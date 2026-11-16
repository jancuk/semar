package fixture

import (
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
)

func policy(t *testing.T, e config.Effects) *effects.Policy {
	t.Helper()
	e.Mode = "test_data"
	p, err := effects.New(config.Environment{
		Hosts:   []string{"api.uat.example.com"},
		Methods: []string{"GET", "POST", "DELETE"},
		Effects: e,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var envServices = map[string]string{"orders": "https://api.uat.example.com", "payments": "https://api.uat.example.com"}

func TestCheckPolicyAcceptsTheExample(t *testing.T) {
	if ps := CheckPolicy(example(t), policy(t, config.Effects{}), envServices); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
}

func TestCheckPolicyServices(t *testing.T) {
	f := example(t)
	ps := CheckPolicy(f, policy(t, config.Effects{}), map[string]string{"orders": "https://api.uat.example.com"})
	if !hasProblem(ps, "create[1]", "service payments is not in this environment") {
		t.Fatalf("problems = %v", ps)
	}
	if len(ps) != 1 {
		t.Errorf("problems = %v, want one", ps)
	}
}

// P2
func TestFindStepThatWrites(t *testing.T) {
	f := example(t)
	f.Find[0].Request.Method = "POST"
	f.Find[0].Request.Path = "/v1/orders/search"
	ps := CheckPolicy(f, policy(t, config.Effects{}), envServices)
	want := "POST /v1/orders/search writes under the effects policy of this environment; a find step must read: list it under effects.reads if it only reads"
	if !hasProblem(ps, "find[0]", want) {
		t.Fatalf("problems = %v", ps)
	}
}

func TestReadsListMakesFindValid(t *testing.T) {
	f := example(t)
	f.Find[0].Request.Method = "POST"
	f.Find[0].Request.Path = "/v1/orders/search"
	reads := config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/v1/orders/search"}}}
	if ps := CheckPolicy(f, policy(t, reads), envServices); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
}

func TestFindStepWithAVariableInThePath(t *testing.T) {
	f := example(t)
	f.Find[0].Request.Method = "POST"
	f.Find[0].Request.Path = "/v1/orders/{{customer}}/search"
	reads := config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/v1/orders/*/search"}}}
	if ps := CheckPolicy(f, policy(t, reads), envServices); len(ps) != 0 {
		t.Fatalf("a pattern with a star must match the placeholder: %v", ps)
	}
	ps := CheckPolicy(f, policy(t, config.Effects{}), envServices)
	if !hasProblem(ps, "find[0]", "POST /v1/orders/{{customer}}/search writes") {
		t.Fatalf("the label holds the path as written: %v", ps)
	}
}

// P3 and P4
func TestOnceAndDeny(t *testing.T) {
	e := config.Effects{
		Once: []config.OpPattern{{Method: "POST", Path: "/v1/vouchers"}},
		Deny: []config.OpPattern{{Method: "POST", Path: "/v1/payments"}, {Method: "POST", Path: "/v1/orders/*/cancel"}},
	}
	f := example(t)
	f.Create[0].Request.Path = "/v1/vouchers"
	ps := CheckPolicy(f, policy(t, e), envServices)
	for _, want := range []struct{ path, msg string }{
		{"create[0]", "POST /v1/vouchers matches effects.once, and a fixture runs on every run"},
		{"create[1]", "POST /v1/payments is on the deny list of this environment"},
		{"cleanup[0]", "POST /v1/orders/{{id}}/cancel is on the deny list of this environment"},
	} {
		if !hasProblem(ps, want.path, want.msg) {
			t.Errorf("missing %s: %s in %v", want.path, want.msg, ps)
		}
	}
}

func TestOnceAndDenyApplyToFind(t *testing.T) {
	e := config.Effects{
		Once: []config.OpPattern{{Method: "GET", Path: "/v1/orders"}},
		Deny: []config.OpPattern{{Method: "GET", Path: "/v1/orders"}},
	}
	ps := CheckPolicy(example(t), policy(t, e), envServices)
	if !hasProblem(ps, "find[0]", "matches effects.once") || !hasProblem(ps, "find[0]", "is on the deny list") {
		t.Fatalf("problems = %v", ps)
	}
}

func TestCheckPolicyWithoutAPolicyChecksServicesOnly(t *testing.T) {
	if ps := CheckPolicy(example(t), nil, map[string]string{"orders": "https://api.uat.example.com"}); len(ps) != 1 || !hasProblem(ps, "create[1]", "service payments") {
		t.Fatalf("problems = %v", ps)
	}
}

// The base URL of a service may carry a path prefix, which the engine keeps in every request. The
// rules are checked against the URL that the engine sends.
func TestCheckPolicyUsesTheBaseURLPrefix(t *testing.T) {
	prefixed := map[string]string{"orders": "https://api.uat.example.com/api", "payments": "https://api.uat.example.com/api"}
	e := config.Effects{
		Once: []config.OpPattern{{Method: "POST", Path: "/api/v1/payments"}},
		Deny: []config.OpPattern{{Method: "POST", Path: "/api/v1/orders/*/cancel"}},
	}
	ps := CheckPolicy(example(t), policy(t, e), prefixed)
	for _, want := range []struct{ path, msg string }{
		{"create[1]", "POST /v1/payments matches effects.once, and a fixture runs on every run"},
		{"cleanup[0]", "POST /v1/orders/{{id}}/cancel is on the deny list of this environment"},
	} {
		if !hasProblem(ps, want.path, want.msg) {
			t.Errorf("missing %s: %s in %v", want.path, want.msg, ps)
		}
	}
	if len(ps) != 2 {
		t.Errorf("problems = %v, want two", ps)
	}

	f := example(t)
	f.Find[0].Request.Method = "POST"
	f.Find[0].Request.Path = "/v1/orders/{{customer}}/search"
	reads := config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/api/v1/orders/*/search"}}}
	if ps := CheckPolicy(f, policy(t, reads), prefixed); len(ps) != 0 {
		t.Errorf("a reads pattern with the prefix must make the find a read: %v", ps)
	}
	unprefixed := config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/v1/orders/*/search"}}}
	if ps := CheckPolicy(f, policy(t, unprefixed), prefixed); !hasProblem(ps, "find[0]", "a find step must read") {
		t.Errorf("a reads pattern without the prefix does not match the sent URL: %v", ps)
	}
}

func TestCheckPolicyBaseURLThatDoesNotParse(t *testing.T) {
	ps := CheckPolicy(example(t), policy(t, config.Effects{}), map[string]string{"orders": "https://api.uat.example.com", "payments": "://bad"})
	if !hasProblem(ps, "create[1]", "the base url of service payments does not parse") {
		t.Fatalf("problems = %v", ps)
	}
}
