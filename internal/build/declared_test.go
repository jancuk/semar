package build_test

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

var _ build.Prober = (*build.Declared)(nil)

func TestDeclaredAnswersForDeclaredServicesOnly(t *testing.T) {
	srv := serve(t, 200, `{"commit":"probed1"}`)
	services := []config.Service{svc("orders", srv.URL, "$.commit"), {Name: "billing", BaseURL: "http://unused"}}
	p := build.NewDeclared(map[string]string{"billing": "abc1234"}, build.NewHTTP(services, nil))

	got, err := p.Probe(context.Background(), "billing")
	if err != nil || got != (domain.BuildRef{Service: "billing", Commit: "abc1234", Declared: true}) {
		t.Errorf("billing = %+v, %v", got, err)
	}
	got, err = p.Probe(context.Background(), "orders")
	if err != nil || got != (domain.BuildRef{Service: "orders", Commit: "probed1"}) {
		t.Errorf("orders = %+v, %v", got, err)
	}
	if _, err := build.NewDeclared(nil, nil).Probe(context.Background(), "orders"); err == nil {
		t.Error("a service with neither a probe nor a declared build was answered")
	}
}

func TestParseDeclared(t *testing.T) {
	probed := svc("orders", "http://unused/version", "$.commit")
	one := []config.Service{probed, {Name: "billing"}}
	two := []config.Service{probed, {Name: "billing"}, {Name: "ledger"}}
	tests := []struct {
		name     string
		values   []string
		services []config.Service
		want     map[string]string
		err      string
	}{
		{"nothing declared", nil, one, nil, ""},
		{"the bare form names the one service with no probe", []string{"0123456789ab"}, one, map[string]string{"billing": "0123456789ab"}, ""},
		{"a named service", []string{"ledger=abcdef0"}, two, map[string]string{"ledger": "abcdef0"}, ""},
		{"two named services", []string{"ledger=abcdef0", "billing=" + strings.Repeat("f", 40)}, two,
			map[string]string{"ledger": "abcdef0", "billing": strings.Repeat("f", 40)}, ""},
		{"the bare form with two services with no probe", []string{"abcdef0"}, two, nil, "billing, ledger have no build probe"},
		{"the bare form when every service has a probe", []string{"abcdef0"}, []config.Service{probed}, nil, "every service of the environment has a build probe"},
		{"a service with a probe", []string{"orders=abcdef0"}, one, nil, "service orders has a build probe"},
		{"an unknown service", []string{"payments=abcdef0"}, one, nil, `no service "payments"; its services are orders, billing`},
		{"an empty service", []string{"=abcdef0"}, one, nil, `no service ""`},
		{"the same service twice", []string{"billing=abcdef0", "billing=abcdef1"}, one, nil, "names service billing more than once"},
		{"the bare and the named form of one service", []string{"abcdef0", "billing=abcdef1"}, one, nil, "more than once"},
		{"a commit that is too short", []string{"abcdef"}, one, nil, "7 to 40 lower-case hexadecimal"},
		{"a commit that is too long", []string{strings.Repeat("a", 41)}, one, nil, "7 to 40"},
		{"an upper-case commit", []string{"ABCDEF0"}, one, nil, "7 to 40 lower-case"},
		{"a branch name", []string{"billing=main"}, one, nil, "7 to 40"},
		{"an empty commit", []string{"billing="}, one, nil, "7 to 40"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := build.ParseDeclared(tt.values, tt.services)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want it to say %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
