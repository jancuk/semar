package fixture

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

func steps(n int, step domain.FixtureStep) []domain.FixtureStep {
	out := make([]domain.FixtureStep, n)
	for i := range out {
		out[i] = step
	}
	return out
}

func str(s string) *string { return &s }

// TestValidateRules has one case for each of the rules F1 to F16. Each case names where the problem is
// and what its text says.
func TestValidateRules(t *testing.T) {
	services := []string{"orders", "payments"}
	cases := []struct {
		rule, name string
		edit       func(f *domain.Fixture)
		file       string // the file name, for rule F3
		path, want string
	}{
		{rule: "F2", name: "api version", edit: func(f *domain.Fixture) { f.APIVersion = "semar/v2" }, path: "apiVersion", want: "apiVersion must be semar/v1"},
		{rule: "F2", name: "kind", edit: func(f *domain.Fixture) { f.Kind = "Scenario" }, path: "kind", want: "kind must be Fixture"},

		{rule: "F3", name: "upper case id", edit: func(f *domain.Fixture) { f.ID = "Order.paid" }, path: "id", want: "id must match ^[a-z][a-z0-9_-]*(\\.[a-z][a-z0-9_-]*)*$"},
		{rule: "F3", name: "id with a slash", edit: func(f *domain.Fixture) { f.ID = "order/paid" }, path: "id", want: "id must match"},
		{rule: "F3", name: "id too long", edit: func(f *domain.Fixture) { f.ID = domain.FixtureID(strings.Repeat("a", 65)) }, path: "id", want: "id is longer than 64 bytes"},
		{rule: "F3", name: "empty id", edit: func(f *domain.Fixture) { f.ID = "" }, path: "id", want: "id must match"},
		{rule: "F3", name: "file name", file: "order-paid.yaml", path: "id", want: "id order.paid does not match the file name order-paid.yaml"},
		{rule: "F3", name: "file name with yml", file: "order.paid.yml"},

		{rule: "F4", name: "no description", edit: func(f *domain.Fixture) { f.Description = "" }, path: "description", want: "description is required"},
		{rule: "F4", name: "blank description", edit: func(f *domain.Fixture) { f.Description = " \n " }, path: "description", want: "description is required"},
		{rule: "F4", name: "too long", edit: func(f *domain.Fixture) { f.Description = strings.Repeat("é", 501) }, path: "description", want: "description has 501 characters; at most 500"},
		{rule: "F4", name: "500 characters", edit: func(f *domain.Fixture) { f.Description = strings.Repeat("é", 500) }},
		{rule: "F4", name: "two paragraphs", edit: func(f *domain.Fixture) { f.Description = "one\n\ntwo" }, path: "description", want: "description must be one paragraph"},
		{rule: "F4", name: "a blank line with spaces", edit: func(f *domain.Fixture) { f.Description = "one\n  \ntwo" }, path: "description", want: "description must be one paragraph"},
		{rule: "F4", name: "folded lines are one paragraph", edit: func(f *domain.Fixture) { f.Description = "one\ntwo" }},
		{rule: "F4", name: "control character", edit: func(f *domain.Fixture) { f.Description = "a\x07b" }, path: "description", want: "description holds a control character"},
		{rule: "F4", name: "carriage return", edit: func(f *domain.Fixture) { f.Description = "a\rb" }, path: "description", want: "description holds a control character"},

		{rule: "F5", name: "version", edit: func(f *domain.Fixture) { f.Version = "1.2" }, path: "version", want: "version 1.2 must be MAJOR.MINOR.PATCH"},
		{rule: "F5", name: "version with v", edit: func(f *domain.Fixture) { f.Version = "v1.2.3" }, path: "version", want: "must be MAJOR.MINOR.PATCH"},
		{rule: "F5", name: "owner", edit: func(f *domain.Fixture) { f.Owner = "team orders" }, path: "owner", want: `invalid owner "team orders"`},
		{rule: "F5", name: "no owner", edit: func(f *domain.Fixture) { f.Owner = "" }, path: "owner", want: "invalid owner"},

		{rule: "F6", name: "reserved run", edit: func(f *domain.Fixture) { f.Params["run"] = domain.Param{} }, path: "params.run", want: "the name run is reserved"},
		{rule: "F6", name: "reserved data", edit: func(f *domain.Fixture) { f.Params["data"] = domain.Param{} }, path: "params.data", want: "the name data is reserved"},
		{rule: "F6", name: "reserved secret", edit: func(f *domain.Fixture) { f.Params["secret"] = domain.Param{} }, path: "params.secret", want: "the name secret is reserved"},
		{rule: "F6", name: "name pattern", edit: func(f *domain.Fixture) { f.Params["Customer"] = domain.Param{} }, path: "params.Customer", want: "name must match ^[a-z][a-z0-9_]*$"},
		{rule: "F6", name: "too many", edit: func(f *domain.Fixture) {
			for i := range 16 {
				f.Params["p"+strings.Repeat("a", i+1)] = domain.Param{}
			}
		}, path: "params", want: "17 parameters; at most 16"},
		{rule: "F6", name: "default with a parameter", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{customer}}")} }, path: "params.a.default", want: "a default is a literal, a literal with {{run.id}} or a date variable, or one {{data.<key>}}"},
		{rule: "F6", name: "default with text around data", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("x-{{data.k}}")} }, path: "params.a.default", want: "a default is a literal"},
		{rule: "F6", name: "default with two data keys", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{data.k}}{{data.j}}")} }, path: "params.a.default", want: "a default is a literal"},
		{rule: "F6", name: "default with a secret", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{secret.TOKEN}}")} }, path: "params.a.default", want: "a default is a literal"},
		{rule: "F6", name: "default with a bad data key", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{data.Customers}}")} }, path: "params.a.default", want: "data key Customers must match"},
		{rule: "F6", name: "default with run id", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("semar-{{run.id}}")} }},
		{rule: "F6", name: "default with a date variable", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("due-{{date.plus7d}}")} }},
		{rule: "F6", name: "default with a date variable and run id", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{local.now}}-{{run.id}}")} }},
		{rule: "F6", name: "default with a name that is not a date variable", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{date.tomorrow}}")} }, path: "params.a.default", want: "a default is a literal"},
		{rule: "F6", name: "default with a malformed variable", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{Default: str("{{run.id")} }, path: "params.a.default", want: "malformed variable"},
		{rule: "F6", name: "no default", edit: func(f *domain.Fixture) { f.Params["a"] = domain.Param{} }},

		{rule: "F7", name: "lease", edit: func(f *domain.Fixture) { f.Lease = "shared" }, path: "lease", want: "lease must be exclusive or empty"},
		{rule: "F7", name: "exclusive", edit: func(f *domain.Fixture) { f.Lease = domain.LeaseExclusive }},

		{rule: "F8", name: "no find and no create", edit: func(f *domain.Fixture) { f.Find, f.Create = nil, nil }, path: "find", want: "a fixture needs find or create steps"},
		{rule: "F8", name: "find only", edit: func(f *domain.Fixture) { f.Create, f.Cleanup = nil, nil }},
		{rule: "F8", name: "create only", edit: func(f *domain.Fixture) { f.Find = nil }},
		{rule: "F8", name: "nine find steps", edit: func(f *domain.Fixture) { f.Find = steps(9, f.Find[0]) }, path: "find", want: "find has 9 steps; at most 8"},
		{rule: "F8", name: "seventeen create steps", edit: func(f *domain.Fixture) { f.Create = steps(17, domain.FixtureStep{Request: f.Create[1].Request}) }, path: "create", want: "create has 17 steps; at most 16"},
		{rule: "F8", name: "nine cleanup steps", edit: func(f *domain.Fixture) { f.Cleanup = steps(9, f.Cleanup[0]) }, path: "cleanup", want: "cleanup has 9 steps; at most 8"},

		{rule: "F9", name: "no service", edit: func(f *domain.Fixture) { f.Create[0].Request.Service = "" }, path: "create[0].request", want: "the request has no service"},
		{rule: "F9", name: "unknown method", edit: func(f *domain.Fixture) { f.Create[0].Request.Method = "FETCH" }, path: "create[0].request", want: "method FETCH is not one of"},
		{rule: "F9", name: "path without a slash", edit: func(f *domain.Fixture) { f.Create[0].Request.Path = "v1/orders" }, path: "create[0].request", want: "path v1/orders must start with /"},
		{rule: "F9", name: "dot segment", edit: func(f *domain.Fixture) { f.Create[0].Request.Path = "/v1/../orders" }, path: "create[0].request", want: "dot segment"},
		{rule: "F9", name: "header name", edit: func(f *domain.Fixture) { f.Create[0].Request.Headers = map[string]string{"X A": "1"} }, path: "create[0].request", want: "is not a valid HTTP header name"},
		{rule: "F9", name: "malformed variable", edit: func(f *domain.Fixture) { f.Create[0].Request.Query = map[string]string{"a": "{{customer"} }, path: "create[0].request", want: "malformed variable"},
		{rule: "F9", name: "problem in cleanup", edit: func(f *domain.Fixture) { f.Cleanup[0].Request.Method = "" }, path: "cleanup[0].request", want: "the request has no method"},

		{rule: "F10", name: "authorization", edit: func(f *domain.Fixture) { f.Create[0].Request.Headers = map[string]string{"Authorization": "x"} }, path: "create[0].request", want: "header Authorization is added by Semar from the service's auth profile and cannot be in a fixture"},
		{rule: "F10", name: "lower case cookie", edit: func(f *domain.Fixture) { f.Find[0].Request.Headers = map[string]string{"cookie": "a=b"} }, path: "find[0].request", want: "header cookie is added by Semar"},
		{rule: "F10", name: "proxy authorization", edit: func(f *domain.Fixture) { f.Cleanup[0].Request.Headers = map[string]string{"proxy-AUTHORIZATION": "x"} }, path: "cleanup[0].request", want: "header proxy-AUTHORIZATION is added by Semar"},
		{rule: "F10", name: "host", edit: func(f *domain.Fixture) { f.Create[0].Request.Headers = map[string]string{"Host": "evil.example"} }, path: "create[0].request", want: "header Host is added by Semar"},
		{rule: "F10", name: "another header", edit: func(f *domain.Fixture) { f.Create[0].Request.Headers = map[string]string{"X-Tenant": "acme"} }},

		{rule: "F11", name: "unknown variable", edit: func(f *domain.Fixture) { f.Create[1].Request.Path = "/v1/payments/{{order}}" }, path: "create[1].request.path", want: "{{order}} is not a parameter, run.id, or a capture of an earlier create step"},
		{rule: "F11", name: "capture of the same step", edit: func(f *domain.Fixture) { f.Create[0].Request.Path = "/v1/orders/{{id}}" }, path: "create[0].request.path", want: "{{id}} is not a parameter"},
		{rule: "F11", name: "capture of a later step", edit: func(f *domain.Fixture) {
			f.Create[1].Request.JSON = map[string]any{"a": "{{later}}"}
			f.Create[2].Captures = []domain.Capture{{Name: "later", JSONPath: "$.x"}}
		}, path: "create[1].request.json.a", want: "{{later}} is not a parameter"},
		{rule: "F11", name: "find capture used by create", edit: func(f *domain.Fixture) {
			f.Create[0].Captures = nil
			f.Create[1].Request.JSON = map[string]any{"orderId": "{{id}}"}
			f.Create = f.Create[:2]
		}, path: "create[1].request.json.orderId", want: "{{id}} is not a parameter"},
		{rule: "F11", name: "create capture used by find", edit: func(f *domain.Fixture) {
			f.Find[0].Request.Query = map[string]string{"a": "{{ref}}"}
			f.Create[0].Captures = append(f.Create[0].Captures, domain.Capture{Name: "ref", JSONPath: "$.ref"})
		}, path: "find[0].request.query a", want: "{{ref}} is not a parameter, run.id, or a capture of an earlier find step"},
		{rule: "F11", name: "cleanup uses a create capture", edit: func(f *domain.Fixture) {}},
		{rule: "F11", name: "cleanup uses a find capture only", edit: func(f *domain.Fixture) {
			f.Create[0].Captures = []domain.Capture{{Name: "other", JSONPath: "$.x"}}
			f.Create = f.Create[:1]
		}, path: "cleanup[0].request.path", want: "{{id}} is not a parameter, run.id, or a capture of an earlier cleanup step or of a create step"},
		{rule: "F11", name: "secret in a header", edit: func(f *domain.Fixture) { f.Create[0].Request.Headers = map[string]string{"X-Key": "{{secret.TOKEN}}"} }, path: "create[0].request.header X-Key", want: "{{secret.TOKEN}} cannot be used in a fixture: the session comes from the auth profile of the service"},
		{rule: "F11", name: "data in a request", edit: func(f *domain.Fixture) {
			f.Create[0].Request.Query = map[string]string{"c": "{{data.customers.default}}"}
		}, path: "create[0].request.query c", want: "{{data.customers.default}} cannot be used in a request: put it in the default of a parameter"},
		{rule: "F11", name: "run id", edit: func(f *domain.Fixture) { f.Create[0].Request.Query = map[string]string{"c": "{{run.id}}"} }},
		{rule: "F11", name: "date variable", edit: func(f *domain.Fixture) { f.Create[0].Request.Query = map[string]string{"d": "{{date.minus7d}}"} }},
		{rule: "F11", name: "date name that is not a variable", edit: func(f *domain.Fixture) { f.Create[0].Request.Query = map[string]string{"d": "{{date.soon}}"} }, path: "create[0].request.query d", want: "{{date.soon}} is not a parameter"},
		{rule: "F11", name: "dotted name that is not a variable", edit: func(f *domain.Fixture) { f.Create[0].Request.Query = map[string]string{"c": "{{order.id}}"} }, path: "create[0].request.query c", want: "{{order.id}} is not a parameter"},

		{rule: "F12", name: "capture name", edit: func(f *domain.Fixture) { f.Create[0].Captures[0].Name = "1id" }, path: "create[0].captures[0]", want: "capture name 1id must match ^[A-Za-z][A-Za-z0-9_]*$"},
		{rule: "F12", name: "capture without a name", edit: func(f *domain.Fixture) { f.Create[0].Captures[0].Name = "" }, path: "create[0].captures[0]", want: "a capture has no name"},
		{rule: "F12", name: "jsonpath", edit: func(f *domain.Fixture) { f.Create[0].Captures[0].JSONPath = "$[" }, path: "create[0].captures[0]", want: "jsonpath does not parse"},
		{rule: "F12", name: "same step twice", edit: func(f *domain.Fixture) {
			f.Create[0].Captures = append(f.Create[0].Captures, domain.Capture{Name: "id", JSONPath: "$.x"})
		}, path: "create[0].captures[1]", want: "capture id is defined more than once in create"},
		{rule: "F12", name: "two steps", edit: func(f *domain.Fixture) {
			f.Create[1].Captures = []domain.Capture{{Name: "id", JSONPath: "$.x"}}
		}, path: "create[1].captures[0]", want: "capture id is defined more than once in create"},
		{rule: "F12", name: "same name in find and create", edit: func(f *domain.Fixture) {}},
		{rule: "F12", name: "name of a parameter", edit: func(f *domain.Fixture) {
			f.Create[1].Captures = []domain.Capture{{Name: "customer", JSONPath: "$.x"}}
		}, path: "create[1].captures[0]", want: "capture customer has the name of a parameter"},

		{rule: "F13", name: "no outputs", edit: func(f *domain.Fixture) { f.Outputs = nil }, path: "outputs", want: "a fixture needs 1 to 16 outputs"},
		{rule: "F13", name: "seventeen outputs", edit: func(f *domain.Fixture) {
			f.Outputs = nil
			for i := range 17 {
				f.Outputs = append(f.Outputs, "o"+strings.Repeat("a", i))
			}
		}, path: "outputs", want: "17 outputs; at most 16"},
		{rule: "F13", name: "not in create", edit: func(f *domain.Fixture) {
			f.Outputs = append(f.Outputs, "total")
			f.Find[0].Captures = append(f.Find[0].Captures, domain.Capture{Name: "total", JSONPath: "$.t"})
		}, path: "outputs[1]", want: "output total is not captured by any create step"},
		{rule: "F13", name: "not in find", edit: func(f *domain.Fixture) {
			f.Outputs = append(f.Outputs, "total")
			f.Create[0].Captures = append(f.Create[0].Captures, domain.Capture{Name: "total", JSONPath: "$.t"})
		}, path: "outputs[1]", want: "output total is not captured by any find step"},
		{rule: "F13", name: "twice", edit: func(f *domain.Fixture) { f.Outputs = []string{"id", "id"} }, path: "outputs[1]", want: "output id is listed more than once"},
		{rule: "F13", name: "find only", edit: func(f *domain.Fixture) { f.Create = nil; f.Cleanup = nil }},
		{rule: "F13", name: "create only", edit: func(f *domain.Fixture) { f.Find = nil }},
		{rule: "F13", name: "name that cannot be captured", edit: func(f *domain.Fixture) { f.Outputs = []string{"a.b"} }, path: "outputs[0]", want: "output a.b is not captured by any find step"},

		{rule: "F14", name: "write", edit: func(f *domain.Fixture) {
			f.Create[1].Until = &domain.Until{JSONPath: "$.a", Equals: "x", Timeout: time.Second}
		}, path: "create[1].until", want: "until is allowed only on a GET or HEAD step: repeating a write would repeat its effect"},
		{rule: "F14", name: "head", edit: func(f *domain.Fixture) { f.Create[2].Request.Method = "HEAD" }},
		{rule: "F14", name: "no timeout", edit: func(f *domain.Fixture) { f.Create[2].Until.Timeout = 0 }, path: "create[2].until.timeout", want: "until needs a timeout"},
		{rule: "F14", name: "negative timeout", edit: func(f *domain.Fixture) { f.Create[2].Until.Timeout = -time.Second }, path: "create[2].until.timeout", want: "until needs a timeout"},
		{rule: "F14", name: "timeout of 15 minutes", edit: func(f *domain.Fixture) { f.Create[2].Until.Timeout = 15 * time.Minute }},
		{rule: "F14", name: "timeout over 15 minutes", edit: func(f *domain.Fixture) { f.Create[2].Until.Timeout = 15*time.Minute + time.Second }, path: "create[2].until.timeout", want: "until timeout 15m1s is above the limit of 15m"},
		{rule: "F14", name: "every under 100ms", edit: func(f *domain.Fixture) { f.Create[2].Until.Every = 99 * time.Millisecond }, path: "create[2].until.every", want: "until every 99ms is under 100ms"},
		{rule: "F14", name: "every of 100ms", edit: func(f *domain.Fixture) { f.Create[2].Until.Every = 100 * time.Millisecond }},
		{rule: "F14", name: "every above timeout", edit: func(f *domain.Fixture) { f.Create[2].Until.Every = time.Minute }, path: "create[2].until.every", want: "until every 1m0s is above the timeout 30s"},
		{rule: "F14", name: "jsonpath", edit: func(f *domain.Fixture) { f.Create[2].Until.JSONPath = "$[" }, path: "create[2].until.jsonpath", want: "jsonpath does not parse"},
		{rule: "F14", name: "no jsonpath", edit: func(f *domain.Fixture) { f.Create[2].Until.JSONPath = "" }, path: "create[2].until.jsonpath", want: "jsonpath does not parse"},
		{rule: "F14", name: "equals a map", edit: func(f *domain.Fixture) { f.Create[2].Until.Equals = map[string]any{"a": 1} }, path: "create[2].until.equals", want: "until equals must be a string, a number, or a boolean"},
		{rule: "F14", name: "equals a list", edit: func(f *domain.Fixture) { f.Create[2].Until.Equals = []any{1} }, path: "create[2].until.equals", want: "until equals must be a string"},
		{rule: "F14", name: "equals nothing", edit: func(f *domain.Fixture) { f.Create[2].Until.Equals = nil }, path: "create[2].until.equals", want: "until equals must be a string"},
		{rule: "F14", name: "equals a number", edit: func(f *domain.Fixture) { f.Create[2].Until.Equals = 3 }},
		{rule: "F14", name: "equals false", edit: func(f *domain.Fixture) { f.Create[2].Until.Equals = false }},
		{rule: "F14", name: "until in find", edit: func(f *domain.Fixture) {
			f.Find[0].Until = &domain.Until{JSONPath: "$.a", Equals: "x", Timeout: time.Second}
		}},

		{rule: "F16", name: "unknown service", edit: func(f *domain.Fixture) { f.Create[1].Request.Service = "ledger" }, path: "create[1].request.service", want: "ledger is not a service of any environment"},
		{rule: "F16", name: "unknown service in find", edit: func(f *domain.Fixture) { f.Find[0].Request.Service = "ledger" }, path: "find[0].request.service", want: "ledger is not a service of any environment"},
		{rule: "F16", name: "unknown service in cleanup", edit: func(f *domain.Fixture) { f.Cleanup[0].Request.Service = "ledger" }, path: "cleanup[0].request.service", want: "ledger is not a service of any environment"},
	}
	for _, tc := range cases {
		t.Run(tc.rule+" "+tc.name, func(t *testing.T) {
			f := example(t)
			if tc.edit != nil {
				tc.edit(&f)
			}
			ps := validate(f, tc.file, services)
			if tc.want == "" {
				if len(ps) != 0 {
					t.Fatalf("problems = %v, want none", ps)
				}
				return
			}
			if !hasProblem(ps, tc.path, tc.want) {
				t.Fatalf("problems = %v, want %s: %s", ps, tc.path, tc.want)
			}
		})
	}
}

func TestValidateAsOfAStep(t *testing.T) {
	f := example(t)
	f.Create[0].As = "carol"
	f.Cleanup[0].As = "carol"
	if ps := Validate(f, nil); len(ps) != 0 {
		t.Fatalf("a step that names an account is valid without a list of accounts: %v", ps)
	}
	if ps := Validate(f, nil, Accounts("default", "carol")); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
	ps := Validate(f, nil, Accounts("default", "bob"))
	if !hasProblem(ps, "create[0].as", "carol is not an auth profile of the configuration: use one of bob, default, or leave out as") ||
		!hasProblem(ps, "cleanup[0].as", "carol is not an auth profile") {
		t.Fatalf("problems = %v", ps)
	}
	if ps := Validate(f, nil, Accounts()); !hasProblem(ps, "create[0].as", "the configuration has no auth profile: leave out as") {
		t.Fatalf("problems = %v", ps)
	}
	f.Create[0].As = "Car ol"
	if ps := Validate(f, nil); !hasProblem(ps, "create[0].as", "auth profile name") {
		t.Fatalf("a bad name is a problem with no list of accounts: %v", ps)
	}
}

func TestValidateWithoutServicesSkipsF16(t *testing.T) {
	f := example(t)
	f.Create[1].Request.Service = "ledger"
	if ps := Validate(f, nil); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
	if ps := Validate(f, []string{}); !hasProblem(ps, "create[1].request.service", "ledger is not a service") {
		t.Fatalf("an empty list of services refuses every service: %v", ps)
	}
}

func TestValidateNamesTheFile(t *testing.T) {
	f := example(t)
	f.Kind = "x"
	f.Owner = ""
	ps := validate(f, "order.paid.yaml", nil)
	if len(ps) < 2 {
		t.Fatalf("problems = %v", ps)
	}
	for _, p := range ps {
		if p.File != "order.paid.yaml" {
			t.Errorf("file = %q", p.File)
		}
	}
	if got := ps[0].String(); !strings.HasPrefix(got, "order.paid.yaml: ") {
		t.Errorf("String = %q", got)
	}
}

func TestProblemString(t *testing.T) {
	for _, tc := range []struct {
		p    Problem
		want string
	}{
		{Problem{"a.yaml", "id", "bad"}, "a.yaml: id: bad"},
		{Problem{"a.yaml", "", "bad"}, "a.yaml: bad"},
		{Problem{"", "id", "bad"}, "id: bad"},
		{Problem{"", "", "bad"}, "bad"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("String = %q, want %q", got, tc.want)
		}
	}
}

func TestSecretVariableRefused(t *testing.T) {
	text := edited(t, `path: /v1/payments, json:`, `path: /v1/payments, headers: { X-Token: "{{secret.TOKEN}}" }, json:`)
	f, err := Parse([]byte(text), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ps := Validate(f, nil)
	if !hasProblem(ps, "create[1].request.header X-Token", "{{secret.TOKEN}} cannot be used in a fixture") {
		t.Fatalf("problems = %v", ps)
	}
	if strings.Contains(strings.ToLower(text), "tok-") {
		t.Fatal("the test text holds a token")
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	f := example(t)
	f.Version = "1"
	f.Create[0].Request.Headers = map[string]string{"Authorization": "x"}
	f.Outputs = []string{"nothing"}
	ps := Validate(f, nil)
	for _, want := range []struct{ path, msg string }{
		{"version", "MAJOR.MINOR.PATCH"},
		{"create[0].request", "header Authorization"},
		{"outputs[0]", "output nothing is not captured by any find step"},
		{"outputs[0]", "output nothing is not captured by any create step"},
	} {
		if !hasProblem(ps, want.path, want.msg) {
			t.Errorf("missing %s: %s in %v", want.path, want.msg, ps)
		}
	}
}
