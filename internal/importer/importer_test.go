package importer_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/importer"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/scenario"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "import", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func shopEnv() config.Environment {
	return config.Environment{
		Hosts: []string{"shop.example"},
		Services: []config.Service{
			{Name: "orders", BaseURL: "http://shop.example", AuthProfile: "agent"},
		},
	}
}

func screen(t *testing.T, cfg config.Redact) *redact.Screen {
	t.Helper()
	s, err := redact.NewScreen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func build(t *testing.T, data []byte, env config.Environment, cfg config.Redact) importer.Built {
	t.Helper()
	s := screen(t, cfg)
	f, err := importer.Read(data, importer.DropHeader(s))
	if err != nil {
		t.Fatal(err)
	}
	return importer.Build(f, importer.Options{File: "in.json", Ticket: "SHOP-77", Env: env, Screen: s})
}

func TestReadFindsTheFormatFromTheContent(t *testing.T) {
	pm, err := importer.Read(fixture(t, "shop.postman_collection.json"), nil)
	if err != nil || pm.Format != importer.Postman {
		t.Fatalf("postman: %v, %v", pm.Format, err)
	}
	har, err := importer.Read(fixture(t, "shop.har"), nil)
	if err != nil || har.Format != importer.HAR {
		t.Fatalf("har: %v, %v", har.Format, err)
	}
}

func TestReadRefusesWithOneSentence(t *testing.T) {
	cases := map[string]struct {
		data string
		want string
	}{
		"not json":      {`<html>`, "the file is neither a Postman collection (format 2.1) nor a HAR file"},
		"other json":    {`{"openapi":"3.0.0","paths":{}}`, "the file is neither a Postman collection (format 2.1) nor a HAR file"},
		"an array":      {`[1,2]`, "the file is neither a Postman collection (format 2.1) nor a HAR file"},
		"postman 2.0":   {`{"info":{"schema":"https://schema.getpostman.com/json/collection/v2.0.0/collection.json"},"item":[]}`, "format 2.1"},
		"har version 2": {`{"log":{"version":"2.0","entries":[]}}`, "version 2.0"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := importer.Read([]byte(c.data), nil)
			if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "\n") {
				t.Errorf("error %v, want one sentence with %q", err, c.want)
			}
		})
	}
}

func TestReadRefusesAFileOverTheLimit(t *testing.T) {
	big := make([]byte, importer.MaxFileBytes+1)
	if _, err := importer.Read(big, nil); err != importer.ErrTooLarge {
		t.Errorf("error %v, want ErrTooLarge", err)
	}
}

func TestReadRefusesTooManyRequests(t *testing.T) {
	var items []string
	for i := range importer.MaxRequests + 1 {
		items = append(items, fmt.Sprintf(`{"name":"r%d","request":{"method":"GET","url":"http://shop.example/v1/o/%d"}}`, i, i))
	}
	data := `{"info":{"schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[` + strings.Join(items, ",") + `]}`
	if _, err := importer.Read([]byte(data), nil); err == nil || !strings.Contains(err.Error(), "split the file") {
		t.Errorf("error %v", err)
	}
}

func TestPostmanBuild(t *testing.T) {
	b := build(t, fixture(t, "shop.postman_collection.json"), shopEnv(), config.Redact{})
	if len(b.Drafts) != 3 {
		t.Fatalf("%d drafts, want 3: %+v", len(b.Drafts), b.Left)
	}
	list, create, noExample := b.Drafts[0], b.Drafts[1], b.Drafts[2]
	if list.Imported.Status != 200 || create.Imported.Status != 201 || noExample.Imported.Status != 0 {
		t.Errorf("statuses %d %d %d", list.Imported.Status, create.Imported.Status, noExample.Imported.Status)
	}
	req := list.Script.Steps[0].Request
	if req.Service != "orders" || req.Path != "/v1/orders" || req.Query["limit"] != "5" || req.Headers["Accept"] != "application/json" {
		t.Errorf("the first request is %+v", req)
	}
	if _, has := req.Headers["Authorization"]; has || len(req.Headers) != 1 {
		t.Errorf("headers %v", req.Headers)
	}
	if body, _ := json.Marshal(create.Script.Steps[0].Request.JSON); string(body) != `{"amount":100000}` {
		t.Errorf("body %s", body)
	}
	if list.Scenario.AuthProfile != "agent" {
		t.Errorf("auth profile %q", list.Scenario.AuthProfile)
	}
	reasons := map[string]string{}
	for _, l := range b.Left {
		reasons[l.Name] = l.Reason
	}
	for name, want := range map[string]string{
		"Orders / Order with no id": "the variable {{orderId}} has no value in the collection",
		"Form":                      "the body is not JSON",
		"Sign in":                   "the body holds a password, a token, or another secret",
		"Other service":             "its host is not a host of the environment",
	} {
		if !strings.Contains(reasons[name], want) {
			t.Errorf("%s: reason %q, want %q", name, reasons[name], want)
		}
	}
	if len(b.OtherHosts) != 1 || b.OtherHosts[0] != "other.example" {
		t.Errorf("other hosts %v", b.OtherHosts)
	}
	f, _ := importer.Read(fixture(t, "shop.postman_collection.json"), nil)
	if f.WithTests != 1 {
		t.Errorf("%d requests with a test script, want 1", f.WithTests)
	}
}

func TestHARBuild(t *testing.T) {
	b := build(t, fixture(t, "shop.har"), shopEnv(), config.Redact{})
	if len(b.Drafts) != 2 {
		t.Fatalf("%d drafts, want 2: %+v", len(b.Drafts), b.Left)
	}
	if b.Drafts[0].Scenario.Title != "GET /v1/orders" || b.Drafts[0].Imported.Status != 200 {
		t.Errorf("first draft %q %d", b.Drafts[0].Scenario.Title, b.Drafts[0].Imported.Status)
	}
	if b.Drafts[1].Imported.Status != 201 {
		t.Errorf("second status %d", b.Drafts[1].Imported.Status)
	}
	if len(b.OtherHosts) != 1 || b.OtherHosts[0] != "cdn.example" {
		t.Errorf("other hosts %v", b.OtherHosts)
	}
	for _, l := range b.Left {
		if strings.Contains(l.Name+l.Reason, "cdn-query-secret") {
			t.Errorf("a left-out name shows a query: %+v", l)
		}
	}
}

func TestNoSecretReachesAScenarioOrAScript(t *testing.T) {
	secrets := []string{
		"tok-collection-secret-7f3a91", "cookie-secret-abc123", "hunter2-login-pw", "c2lnbmF0dXJl",
		"har-token-secret-9d2c41", "har-cookie-secret-77", "cdn-query-secret",
	}
	for _, name := range []string{"shop.postman_collection.json", "shop.har"} {
		b := build(t, fixture(t, name), shopEnv(), config.Redact{})
		var all strings.Builder
		for _, d := range b.Drafts {
			sc, s, err := d.Finalize("SHOP-77-I01", 200)
			if err != nil {
				t.Fatal(err)
			}
			feature, err := scenario.Render(sc, "")
			if err != nil {
				t.Fatal(err)
			}
			script, _ := json.Marshal(s)
			all.Write(feature)
			all.Write(script)
		}
		for _, l := range b.Left {
			all.WriteString(l.Name + l.Reason)
		}
		for _, s := range secrets {
			if strings.Contains(all.String(), s) {
				t.Errorf("%s: the output holds %q", name, s)
			}
		}
	}
}

func TestHeadersTheRedactionRulesNameAreDropped(t *testing.T) {
	data := []byte(`{"info":{"schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
	"variable":[{"key":"tenantToken","value":"tenant-abc"}],
	"item":[{"name":"x","request":{"method":"GET","url":"http://shop.example/v1/a","header":[
		{"key":"X-Tenant","value":"acme"},{"key":"X-Custom-Id","value":"{{tenantToken}}"},
		{"key":"X-Api-Key","value":"abc"},{"key":"Proxy-Authorization","value":"Basic x"},{"key":"Accept","value":"*/*","disabled":true}]},
	"response":[{"code":200}]}]}`)
	b := build(t, data, shopEnv(), config.Redact{Headers: []string{"X-Tenant"}})
	if len(b.Drafts) != 1 {
		t.Fatalf("%+v", b.Left)
	}
	if h := b.Drafts[0].Script.Steps[0].Request.Headers; len(h) != 0 {
		t.Errorf("headers kept: %v", h)
	}
}

func TestAConfiguredJSONPathKeepsTheRequestOut(t *testing.T) {
	data := []byte(`{"log":{"version":"1.2","entries":[{"request":{"method":"POST","url":"http://shop.example/v1/a","headers":[],"postData":{"mimeType":"application/json","text":"{\"customer\":{\"nik\":\"3174000000000001\"}}"}},"response":{"status":200,"headers":[],"content":{"mimeType":"application/json"}}}]}}`)
	b := build(t, data, shopEnv(), config.Redact{JSONPaths: []string{"$.customer.nik"}})
	if len(b.Drafts) != 0 || len(b.Left) != 1 || !strings.Contains(b.Left[0].Reason, "secret") {
		t.Errorf("%+v %+v", b.Drafts, b.Left)
	}
}

func TestRequestsThatCarrySecretsInTheAddress(t *testing.T) {
	for name, url := range map[string]string{
		"query key":  "http://shop.example/v1/a?access_token=zzzz",
		"query jwt":  "http://shop.example/v1/a?x=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.c2ln",
		"user info":  "http://qa:pw@shop.example/v1/a",
		"repeat key": "http://shop.example/v1/a?x=1&x=2",
	} {
		t.Run(name, func(t *testing.T) {
			data := fmt.Sprintf(`{"info":{"schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[{"name":"x","request":{"method":"GET","url":%q},"response":[{"code":200}]}]}`, url)
			b := build(t, []byte(data), shopEnv(), config.Redact{})
			if len(b.Drafts) != 0 || len(b.Left) != 1 {
				t.Errorf("%+v", b)
			}
			if strings.Contains(b.Left[0].Reason, "zzzz") || strings.Contains(b.Left[0].Name, "pw@") {
				t.Errorf("the reason shows a value: %+v", b.Left[0])
			}
		})
	}
}

func TestAServiceWithAPathPrefix(t *testing.T) {
	env := config.Environment{
		Hosts:    []string{"shop.example"},
		Services: []config.Service{{Name: "root", BaseURL: "http://shop.example"}, {Name: "v2", BaseURL: "http://shop.example/api/v2"}},
	}
	data := []byte(`{"log":{"version":"1.2","entries":[
	{"request":{"method":"GET","url":"http://shop.example/api/v2/orders","headers":[]},"response":{"status":200,"headers":[],"content":{"mimeType":"application/json"}}},
	{"request":{"method":"GET","url":"http://shop.example/api/v20/orders","headers":[]},"response":{"status":200,"headers":[],"content":{"mimeType":"application/json"}}}]}}`)
	b := build(t, data, env, config.Redact{})
	if len(b.Drafts) != 2 {
		t.Fatalf("%+v", b.Left)
	}
	first, second := b.Drafts[0].Script.Steps[0].Request, b.Drafts[1].Script.Steps[0].Request
	if first.Service != "v2" || first.Path != "/orders" || second.Service != "root" || second.Path != "/api/v20/orders" {
		t.Errorf("%+v %+v", first, second)
	}
}

func TestAnImportedExpectationAlwaysNeedsAReview(t *testing.T) {
	e := domain.Expectation{Check: domain.Check{Status: new(int)}, Source: domain.SourceImport}
	if !confirm.NeedsExpectation(e, nil) {
		t.Error("an imported expectation needs no review")
	}
	if !confirm.NeedsExpectation(e, []domain.Source{domain.SourceDiff}) {
		t.Error("a trust list lifted the review of an imported expectation")
	}
}

func TestFinalizeWritesAScenarioThatParsesBack(t *testing.T) {
	b := build(t, fixture(t, "shop.har"), shopEnv(), config.Redact{})
	sc, _, err := b.Drafts[0].Finalize("SHOP-77-I01", 200)
	if err != nil {
		t.Fatal(err)
	}
	data, err := scenario.Render(sc, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := scenario.Parse(data, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].Expect[0].Source != domain.SourceImport || got.AuthProfile != "agent" {
		t.Errorf("%+v", got)
	}
	if _, _, err := b.Drafts[0].Finalize("SHOP-77-I02", 99); err == nil {
		t.Error("a status of 99 was accepted")
	}
}

func TestKnownRequestsAreNotImportedAgain(t *testing.T) {
	s := screen(t, config.Redact{})
	f, err := importer.Read(fixture(t, "shop.har"), importer.DropHeader(s))
	if err != nil {
		t.Fatal(err)
	}
	opts := importer.Options{File: "in.har", Ticket: "SHOP-77", Env: shopEnv(), Screen: s}
	first := importer.Build(f, opts)
	sc, _, err := first.Drafts[0].Finalize("SHOP-77-I01", 200)
	if err != nil {
		t.Fatal(err)
	}
	opts.Known = importer.Fingerprints([]domain.Scenario{sc})
	again := importer.Build(f, opts)
	if len(again.Drafts) != 1 || len(again.Left) != 2 {
		t.Errorf("%d drafts and %+v", len(again.Drafts), again.Left)
	}
}
