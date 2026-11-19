package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

// Marker is the text that an imported scenario carries in its description, so that a second import of
// the same request finds it. The fingerprint follows it.
const Marker = "Import fingerprint "

var markerLine = regexp.MustCompile(`Import fingerprint ([0-9a-f]{12})`)

// Fingerprints are the fingerprints that the descriptions of scenarios hold.
func Fingerprints(scs []domain.Scenario) map[string]string {
	out := map[string]string{}
	for _, sc := range scs {
		if m := markerLine.FindStringSubmatch(sc.Intent); m != nil {
			out[m[1]] = string(sc.ID)
		}
	}
	return out
}

// noisyHeaders are headers of a browser or of the connection that no test needs, and the ones that the
// script or the engine sets.
var noisyHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true, "connection": true, "upgrade": true,
	"te": true, "trailer": true, "keep-alive": true, "proxy-connection": true, "traceparent": true,
	"baggage": true, "user-agent": true, "x-semar-run": true, "accept-encoding": true, "origin": true,
	"referer": true, "dnt": true, "upgrade-insecure-requests": true, "cache-control": true, "pragma": true,
	"priority": true, "if-modified-since": true, "x-forwarded-for": true, "x-forwarded-proto": true,
	"x-forwarded-host": true, "x-real-ip": true,
}

// DropHeader says which header of a file Semar never copies: the session, every header that the
// redaction rules name or that a secret's name marks, and the headers that a browser or the engine sets.
func DropHeader(s *redact.Screen) func(string) bool {
	return func(name string) bool {
		l := strings.ToLower(name)
		return noisyHeaders[l] || strings.HasPrefix(l, "sec-") || s.Header(name) || redact.Name(name)
	}
}

// Options say what the requests become.
type Options struct {
	File   string // the base name of the file, for the cite and the description
	Ticket string
	Env    config.Environment // the services carry their auth profile
	Screen *redact.Screen
	// Known holds the fingerprints of requests that are imported already.
	Known map[string]string
}

// Draft is a request that fits the environment, as a scenario with no expected result yet and its script.
type Draft struct {
	Imported    Imported
	Fingerprint string
	Scenario    domain.Scenario
	Script      domain.Script
}

// Built is what Build made of a file.
type Built struct {
	Drafts     []Draft
	Left       []Left
	OtherHosts []string // hosts of the file that the environment does not list, sorted, with no duplicates
}

var knownMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

// Build turns requests into drafts. A request that cannot be copied safely, or does not belong to the
// environment, is left out with its reason, and a reason never holds a value of the request.
func Build(f File, o Options) Built {
	b := Built{Left: slices.Clone(f.Left)}
	hosts := map[string]bool{}
	seen := map[string]bool{}
	for _, imp := range f.Requests {
		d, why, other := draft(imp, o)
		if other != "" {
			hosts[other] = true
		}
		switch {
		case why != "":
			b.Left = append(b.Left, Left{Name: label(imp), Reason: why})
		case seen[d.Fingerprint]:
		case o.Known[d.Fingerprint] != "":
			b.Left = append(b.Left, Left{Name: label(imp), Reason: "it is imported already, as " + o.Known[d.Fingerprint]})
		default:
			seen[d.Fingerprint] = true
			b.Drafts = append(b.Drafts, d)
		}
	}
	b.OtherHosts = slices.Sorted(maps.Keys(hosts))
	return b
}

func label(imp Imported) string {
	if imp.Name != "" {
		return oneLine(imp.Name)
	}
	return imp.Method + " " + imp.URL
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 100 {
		s = string([]rune(s)[:99]) + "…"
	}
	return s
}

func draft(imp Imported, o Options) (d Draft, why, otherHost string) {
	u, err := url.Parse(imp.URL)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
		return d, "the request has no address that Semar can send to", ""
	case u.User != nil:
		return d, "the address holds a user name or a password, and Semar copies no secret", ""
	case !hostrule.Allowed(u, o.Env.Hosts):
		return d, "its host is not a host of the environment", hostrule.Entry(u)
	}
	svc, rel, ok := service(u, o.Env.Services)
	if !ok {
		return d, "no service of the environment serves its host and path", ""
	}
	if !slices.Contains(knownMethods, imp.Method) {
		return d, "Semar does not send the method " + imp.Method, ""
	}
	if strings.ContainsAny(rel, "{}") || strings.Contains(imp.URL, "{{") {
		return d, "the address still holds a variable", ""
	}

	query := map[string]string{}
	for k, vs := range u.Query() {
		if len(vs) > 1 {
			return d, "the query repeats the key " + k + ", which a script cannot hold", ""
		}
		if redact.Name(k) || o.Screen.Value(vs[0]) {
			return d, "the query holds a secret, and Semar copies no secret", ""
		}
		query[k] = vs[0]
	}

	var body any
	if len(bytes.TrimSpace(imp.Body)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(imp.Body))
		dec.UseNumber()
		if err := dec.Decode(&body); err != nil || dec.More() {
			return d, "the body is not JSON", ""
		}
		if holdsSecret(body, o.Screen) || o.Screen.Paths(body) {
			return d, "the body holds a password, a token, or another secret, and Semar copies no secret", ""
		}
		if body, ok = plainNumbers(body); !ok {
			return d, "a number of the body is too large to keep exactly", ""
		}
	}

	headers := map[string]string{}
	for _, h := range imp.Headers {
		if DropHeader(o.Screen)(h.Name) || o.Screen.Value(h.Value) {
			continue
		}
		if strings.Contains(h.Value, "{{") {
			return d, "a header still holds a variable", ""
		}
		if prev, dup := headers[h.Name]; dup {
			headers[h.Name] = prev + ", " + h.Value
			continue
		}
		headers[h.Name] = h.Value
	}

	req := domain.APIRequest{Service: svc.Name, Method: imp.Method, Path: rel}
	if len(query) > 0 {
		req.Query = query
	}
	if len(headers) > 0 {
		req.Headers = headers
	}
	if body != nil {
		req.JSON = body
	}

	title := oneLine(imp.Name)
	if title == "" || strings.HasPrefix(imp.Name, imp.Method+" http") || strings.HasPrefix(imp.Name, imp.Method+" (") {
		title = imp.Method + " " + rel
	}
	fp := fingerprint(req)
	sc := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", Title: title, Ticket: o.Ticket, Surface: domain.SurfaceAPI,
		AuthProfile: svc.AuthProfile,
		Intent:      fmt.Sprintf("Imported from %s. %s%s", oneLine(o.File), Marker, fp),
		Steps: []domain.Step{{
			ID: "send", Action: "Send " + imp.Method + " " + rel,
			Hint: &domain.RequestHint{Method: imp.Method, Path: rel},
		}},
	}
	d = Draft{
		Imported: imp, Fingerprint: fp, Scenario: sc,
		Script: domain.Script{
			APIVersion: "semar/v1", Kind: "Script",
			AuthoredBy: domain.ProviderRef{Instance: "import", Model: "none", Harness: "none"},
			Steps:      []domain.ScriptStep{{Step: "send", Request: &req}},
		},
	}
	return d, "", ""
}

// service finds the service that serves u: the one whose base URL has the same host and the longest
// path prefix of the path of u. rel is the path below that base URL.
func service(u *url.URL, services []config.Service) (config.Service, string, bool) {
	var best config.Service
	var bestRel string
	bestLen := -1
	for _, s := range services {
		base, err := url.Parse(s.BaseURL)
		if err != nil || !hostrule.Allowed(u, []string{hostrule.Entry(base)}) {
			continue
		}
		prefix := strings.TrimSuffix(base.EscapedPath(), "/")
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		rel, ok := strings.CutPrefix(path, prefix)
		if !ok || (rel != "" && !strings.HasPrefix(rel, "/")) {
			continue
		}
		if rel == "" {
			rel = "/"
		}
		if len(prefix) > bestLen {
			best, bestRel, bestLen = s, rel, len(prefix)
		}
	}
	return best, bestRel, bestLen >= 0
}

// plainNumbers turns every json.Number into the int or float64 that a script holds, because the store
// writes a script as YAML and would write a json.Number as text.
func plainNumbers(v any) (any, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), int64(int(i)) == i
		}
		f, err := x.Float64()
		return f, err == nil && strings.ContainsAny(string(x), ".eE")
	case map[string]any:
		for k, e := range x {
			n, ok := plainNumbers(e)
			if !ok {
				return nil, false
			}
			x[k] = n
		}
	case []any:
		for i, e := range x {
			n, ok := plainNumbers(e)
			if !ok {
				return nil, false
			}
			x[i] = n
		}
	}
	return v, true
}

func holdsSecret(v any, s *redact.Screen) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if redact.Name(k) || holdsSecret(e, s) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(x, func(e any) bool { return holdsSecret(e, s) })
	case string:
		return s.Value(x)
	}
	return false
}

func fingerprint(req domain.APIRequest) string {
	var b strings.Builder
	b.WriteString(req.Method + " " + req.Service + " " + req.Path)
	for _, k := range slices.Sorted(maps.Keys(req.Query)) {
		b.WriteString("?" + k + "=" + req.Query[k])
	}
	if req.JSON != nil {
		j, _ := json.Marshal(req.JSON)
		b.Write(j)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:12]
}

// Finalize gives a draft its ID and its expected status, and checks that Semar can write and read it
// back. The returned scenario and script hold the hash of the expected results.
func (d Draft) Finalize(id domain.ScenarioID, status int) (domain.Scenario, domain.Script, error) {
	if status < 100 || status > 599 {
		return d.Scenario, d.Script, fmt.Errorf("the status %d is not an HTTP status", status)
	}
	sc := d.Scenario
	sc.ID = id
	sc.Steps = slices.Clone(sc.Steps)
	sc.Steps[0].Expect = []domain.Expectation{{
		Check: domain.Check{Status: &status}, Source: domain.SourceImport,
	}}
	s := d.Script
	s.Scenario = id
	s.ExpectHash = domain.ExpectHash(sc)
	if _, err := scenario.Render(sc, ""); err != nil {
		return sc, s, fmt.Errorf("it cannot be written as a scenario: %w", err)
	}
	var problems []string
	for _, p := range script.Validate(sc, s) {
		problems = append(problems, p.Message)
	}
	if len(problems) > 0 {
		return sc, s, fmt.Errorf("its script is not valid: %s", strings.Join(problems, "; "))
	}
	return sc, s, nil
}
