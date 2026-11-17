package domain_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

func fullFixture() domain.Fixture {
	def := "{{data.customers.default}}"
	return domain.Fixture{
		APIVersion:  "semar/v1",
		Kind:        "Fixture",
		ID:          "order.paid",
		Description: "A paid order of 1000",
		Version:     "1.0.0",
		Owner:       "team-orders",
		Params:      map[string]domain.Param{"customer": {Default: &def}, "note": {}},
		Lease:       domain.LeaseExclusive,
		Find: []domain.FixtureStep{{
			Request:  domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders", Query: map[string]string{"status": "paid"}, Headers: map[string]string{"Accept": "application/json"}, JSON: map[string]any{"a": 1}},
			Captures: []domain.Capture{{Name: "id", JSONPath: "$.data[0].id"}},
		}},
		Create: []domain.FixtureStep{{
			Request:  domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{id}}"},
			Captures: []domain.Capture{{Name: "id", JSONPath: "$.data.id"}},
			Until:    &domain.Until{JSONPath: "$.data.status", Equals: "paid", Timeout: 30 * time.Second, Every: time.Second},
		}},
		Outputs: []string{"id"},
		Cleanup: []domain.FixtureStep{{Request: domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders/{{id}}/cancel"}}},
	}
}

func TestFixtureHash(t *testing.T) {
	h := domain.FixtureHash(fullFixture())
	if len(h) != 64 {
		t.Fatalf("want a SHA-256 hex string, got %q", h)
	}
	if domain.FixtureHash(fullFixture()) != h {
		t.Fatal("the hash must be stable")
	}
}

// The value must change only on purpose: a change lapses every stored approval.
func TestFixtureHashGolden(t *testing.T) {
	const want = "13f7d1769d04565394260f3a7862e7c0ffb50c0dee087640e3468b32e19b1d4b"
	if got := domain.FixtureHash(fullFixture()); string(got) != want {
		t.Fatalf("FixtureHash(fullFixture()) = %s, want %s", got, want)
	}
}

func TestFixtureHashCoversEveryField(t *testing.T) {
	h := domain.FixtureHash(fullFixture())

	other := "other"
	changes := map[string]func(*domain.Fixture){
		"ID":                  func(f *domain.Fixture) { f.ID = "order.other" },
		"Description":         func(f *domain.Fixture) { f.Description += "!" },
		"Version":             func(f *domain.Fixture) { f.Version = "1.0.1" },
		"Owner":               func(f *domain.Fixture) { f.Owner = "team-x" },
		"Params":              func(f *domain.Fixture) { f.Params["extra"] = domain.Param{} },
		"Params.Default":      func(f *domain.Fixture) { f.Params["customer"] = domain.Param{Default: &other} },
		"Params.Default.nil":  func(f *domain.Fixture) { f.Params["customer"] = domain.Param{} },
		"Params.Default.zero": func(f *domain.Fixture) { e := ""; f.Params["note"] = domain.Param{Default: &e} },
		"Lease":               func(f *domain.Fixture) { f.Lease = "" },
		"Find":                func(f *domain.Fixture) { f.Find = nil },
		"Find.Request.Service": func(f *domain.Fixture) {
			f.Find[0].Request.Service = "payments"
		},
		"Find.Request.Method":  func(f *domain.Fixture) { f.Find[0].Request.Method = "HEAD" },
		"Find.Request.Path":    func(f *domain.Fixture) { f.Find[0].Request.Path = "/v2/orders" },
		"Find.Request.Query":   func(f *domain.Fixture) { f.Find[0].Request.Query["status"] = "open" },
		"Find.Request.Headers": func(f *domain.Fixture) { f.Find[0].Request.Headers["Accept"] = "text/plain" },
		"Find.Request.JSON":    func(f *domain.Fixture) { f.Find[0].Request.JSON = map[string]any{"a": 2} },
		"Find.Captures.Name":   func(f *domain.Fixture) { f.Find[0].Captures[0].Name = "oid" },
		"Find.Captures.JSONPath": func(f *domain.Fixture) {
			f.Find[0].Captures[0].JSONPath = "$.data[1].id"
		},
		"Find.As":                func(f *domain.Fixture) { f.Find[0].As = "carol" },
		"Create.As":              func(f *domain.Fixture) { f.Create[0].As = "carol" },
		"Cleanup.As":             func(f *domain.Fixture) { f.Cleanup[0].As = "carol" },
		"Find.Captures.Element":  func(f *domain.Fixture) { f.Find[0].Captures[0].Element = "title" },
		"Create":                 func(f *domain.Fixture) { f.Create = nil },
		"Create.Until":           func(f *domain.Fixture) { f.Create[0].Until = nil },
		"Create.Until.JSONPath":  func(f *domain.Fixture) { f.Create[0].Until.JSONPath = "$.data.state" },
		"Create.Until.Equals":    func(f *domain.Fixture) { f.Create[0].Until.Equals = "open" },
		"Create.Until.Timeout":   func(f *domain.Fixture) { f.Create[0].Until.Timeout = time.Minute },
		"Create.Until.Every":     func(f *domain.Fixture) { f.Create[0].Until.Every = 2 * time.Second },
		"Outputs":                func(f *domain.Fixture) { f.Outputs = []string{"id", "email"} },
		"Cleanup":                func(f *domain.Fixture) { f.Cleanup = nil },
		"Cleanup.Request.Method": func(f *domain.Fixture) { f.Cleanup[0].Request.Method = "DELETE" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := fullFixture()
			change(&f)
			if domain.FixtureHash(f) == h {
				t.Errorf("%s must change the hash", name)
			}
		})
	}

	// Every field of Fixture other than the two that name the file format has a case above.
	typ := reflect.TypeOf(domain.Fixture{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if name == "APIVersion" || name == "Kind" {
			continue
		}
		if _, ok := changes[name]; !ok {
			t.Errorf("Fixture.%s has no case: a new field must change the hash", name)
		}
	}
	for _, c := range []struct {
		prefix string
		typ    reflect.Type
		skip   string
	}{
		{"Find.Request.", reflect.TypeOf(domain.APIRequest{}), ""},
		{"Find.Captures.", reflect.TypeOf(domain.Capture{}), ""},
		{"Create.Until.", reflect.TypeOf(domain.Until{}), ""},
		{"Params.", reflect.TypeOf(domain.Param{}), ""},
	} {
		for i := range c.typ.NumField() {
			name := c.prefix + c.typ.Field(i).Name
			if _, ok := changes[name]; !ok {
				t.Errorf("%s has no case: a new field must change the hash", name)
			}
		}
	}

	for name, change := range map[string]func(*domain.Fixture){
		"apiVersion": func(f *domain.Fixture) { f.APIVersion = "semar/v2" },
		"kind":       func(f *domain.Fixture) { f.Kind = "Other" },
	} {
		f := fullFixture()
		change(&f)
		if domain.FixtureHash(f) != h {
			t.Errorf("%s must not change the hash", name)
		}
	}
}

func TestFixtureHashEncodingFailureGivesEmptyHash(t *testing.T) {
	f := fullFixture()
	f.Find[0].Request.JSON = make(chan int)
	if got := domain.FixtureHash(f); got != "" {
		t.Fatalf("FixtureHash with an unencodable body = %q, want empty", got)
	}
}

func TestFixtureHashIgnoresMapOrder(t *testing.T) {
	a, b := fullFixture(), fullFixture()
	a.Find[0].Request.Query = map[string]string{"a": "1", "b": "2", "c": "3"}
	b.Find[0].Request.Query = map[string]string{"c": "3", "b": "2", "a": "1"}
	if domain.FixtureHash(a) != domain.FixtureHash(b) {
		t.Error("map order must not change the hash")
	}
}

func TestFixtureRecordNeverStoresRawOutputs(t *testing.T) {
	typ := reflect.TypeOf(domain.GivenFind{})
	f, _ := typ.FieldByName("Outputs")
	if tag := f.Tag.Get("json"); !strings.HasPrefix(tag, "-") {
		t.Fatalf("GivenFind.Outputs holds raw values and must not be encoded, tag %q", tag)
	}
}
