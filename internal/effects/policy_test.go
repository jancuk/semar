package effects_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
)

func env(mode string) config.Environment {
	return config.Environment{
		Hosts:   []string{"api.uat.example.com"},
		Methods: []string{"GET", "POST", "DELETE"},
		Effects: config.Effects{
			Mode:  mode,
			Deny:  []config.OpPattern{{Method: "POST", Path: "/v1/payouts/**"}},
			Once:  []config.OpPattern{{Method: "POST", Path: "/v1/vouchers/*/redeem"}},
			Reads: []config.OpPattern{{Method: "POST", Path: "/v1/search"}},
		},
	}
}

func req(t *testing.T, method, raw string) effects.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return effects.Request{Method: method, URL: u}
}

func TestAdmit(t *testing.T) {
	const h = "https://api.uat.example.com"
	cases := []struct{ name, mode, method, url, want string }{
		{"get", "test_data", "GET", h + "/v1/orders/1", ""},
		{"post", "test_data", "POST", h + "/v1/orders", ""},
		{"denied", "test_data", "POST", h + "/v1/payouts/7/send", "denied"},
		{"method not allowed", "test_data", "PUT", h + "/v1/orders/1", "method_not_allowed"},
		{"read only write", "read_only", "POST", h + "/v1/orders", "read_only"},
		{"read only reads pattern", "read_only", "POST", h + "/v1/search", ""},
		{"read only get", "read_only", "GET", h + "/v1/orders/1", ""},
		{"user info", "test_data", "GET", "https://api.uat.example.com@evil.example/v1/orders", "host_not_allowed"},
		{"upper case host", "test_data", "GET", "https://API.UAT.EXAMPLE.COM/v1/orders/1", ""},
		{"trailing dot", "test_data", "GET", "https://api.uat.example.com./v1/orders/1", ""},
		{"explicit default port", "test_data", "GET", h + ":443/v1/orders/1", ""},
		{"other port", "test_data", "GET", h + ":8443/v1/orders/1", "host_not_allowed"},
		{"suffix host", "test_data", "GET", "https://api.uat.example.com.evil.example/v1", "host_not_allowed"},
		{"loopback", "test_data", "GET", "http://127.0.0.1/v1", "host_not_allowed"},
		{"ftp scheme", "test_data", "GET", "ftp://api.uat.example.com/v1", "host_not_allowed"},
		{"no scheme", "test_data", "GET", "//api.uat.example.com/v1", "host_not_allowed"},
		{"lower case method", "test_data", "get", h + "/v1/orders/1", ""},
		{"lower case denied method", "test_data", "post", h + "/v1/payouts/7/send", "denied"},
		{"dot dot", "test_data", "POST", h + "/v1/orders/../payouts/7/send", "denied"},
		{"dot segment", "test_data", "POST", h + "/v1/./orders", "denied"},
		{"encoded slash", "test_data", "POST", h + "/v1/payouts%2f7/send", "denied"},
		{"encoded slash upper", "test_data", "POST", h + "/v1/orders%2F1", "denied"},
		{"encoded dot dot", "test_data", "POST", h + "/v1/orders/%2e%2e/payouts/7/send", "denied"},
		{"double slash", "test_data", "POST", h + "/v1//payouts/7/send", "denied"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := effects.New(env(c.mode))
			if err != nil {
				t.Fatal(err)
			}
			err = p.Admit(req(t, c.method, c.url))
			if c.want == "" {
				if err != nil {
					t.Fatalf("Admit = %v, want nil", err)
				}
				return
			}
			var ref *effects.Refusal
			if !errors.As(err, &ref) {
				t.Fatalf("Admit = %v, want a *Refusal", err)
			}
			if string(ref.Kind) != c.want {
				t.Fatalf("kind = %q, want %q (%s)", ref.Kind, c.want, ref.Detail)
			}
			if msg := err.Error(); msg != strings.ToLower(msg) || !strings.Contains(msg, c.want) || !strings.Contains(msg, strings.ToLower(ref.Detail)) {
				t.Fatalf("Error() = %q", msg)
			}
		})
	}
}

func TestAdmitNilURL(t *testing.T) {
	p, _ := effects.New(env("test_data"))
	var ref *effects.Refusal
	if err := p.Admit(effects.Request{Method: "GET"}); !errors.As(err, &ref) || ref.Kind != effects.HostNotAllowed {
		t.Fatalf("Admit = %v, want host_not_allowed", err)
	}
}

func TestAdmitPathNotNormalDetail(t *testing.T) {
	p, _ := effects.New(env("test_data"))
	var ref *effects.Refusal
	err := p.Admit(req(t, "POST", "https://api.uat.example.com/v1/orders/../payouts/7/send"))
	if !errors.As(err, &ref) || !strings.Contains(ref.Detail, "not normal") {
		t.Fatalf("Admit = %v, want a detail saying the path is not normal", err)
	}
}

func TestAdmitPortEntries(t *testing.T) {
	e := env("test_data")
	e.Hosts = []string{"127.0.0.1:8080", "api.uat.example.com"}
	p, err := effects.New(e)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, url string
		ok        bool
	}{
		{"entry port", "http://127.0.0.1:8080/v1", true},
		{"entry port, default port requested", "http://127.0.0.1/v1", false},
		{"entry port, other port", "http://127.0.0.1:9090/v1", false},
		{"plain entry over http default", "http://api.uat.example.com/v1", true},
		{"plain entry over http port 80", "http://api.uat.example.com:80/v1", true},
		{"plain entry over http port 443", "http://api.uat.example.com:443/v1", false},
		{"plain entry over https port 80", "https://api.uat.example.com:80/v1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Admit(req(t, "GET", c.url)) == nil; got != c.ok {
				t.Fatalf("admitted = %v, want %v", got, c.ok)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	p, err := effects.New(env("test_data"))
	if err != nil {
		t.Fatal(err)
	}
	const h = "https://api.uat.example.com"
	cases := []struct {
		name, method, url string
		want              effects.Class
	}{
		{"get", "GET", h + "/v1/orders", effects.Read},
		{"head", "HEAD", h + "/v1/orders", effects.Read},
		{"lower case get", "get", h + "/v1/orders", effects.Read},
		{"reads pattern", "POST", h + "/v1/search", effects.Read},
		{"post", "POST", h + "/v1/orders", effects.Write},
		{"put", "PUT", h + "/v1/orders/1", effects.Write},
		{"delete", "DELETE", h + "/v1/orders/1", effects.Write},
		{"patch", "PATCH", h + "/v1/orders/1", effects.Write},
		{"search with dot dot", "POST", h + "/v1/x/../search", effects.Write},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Classify(req(t, c.method, c.url)); got != c.want {
				t.Fatalf("Classify = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOnce(t *testing.T) {
	p, err := effects.New(env("test_data"))
	if err != nil {
		t.Fatal(err)
	}
	const h = "https://api.uat.example.com"
	cases := []struct {
		name, method, url string
		want              bool
	}{
		{"match", "POST", h + "/v1/vouchers/9/redeem", true},
		{"lower case method", "post", h + "/v1/vouchers/9/redeem", true},
		{"wrong method", "GET", h + "/v1/vouchers/9/redeem", false},
		{"wrong path", "POST", h + "/v1/vouchers/9", false},
		{"deeper path", "POST", h + "/v1/vouchers/9/x/redeem", false},
		{"not normal path", "POST", h + "/v1/vouchers/../vouchers/9/redeem", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Once(req(t, c.method, c.url)); got != c.want {
				t.Fatalf("Once = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Environment)
		wantErr string
	}{
		{"valid", func(*config.Environment) {}, ""},
		{"bad mode", func(e *config.Environment) { e.Effects.Mode = "write" }, "mode"},
		{"empty mode", func(e *config.Environment) { e.Effects.Mode = "" }, "mode"},
		{"bad deny", func(e *config.Environment) { e.Effects.Deny = []config.OpPattern{{Method: "POST", Path: "/v1/[x"}} }, "deny[0]"},
		{"bad once", func(e *config.Environment) {
			e.Effects.Once = append(e.Effects.Once, config.OpPattern{Method: "POST", Path: "/a/[x"})
		}, "once[1]"},
		{"bad reads", func(e *config.Environment) { e.Effects.Reads = []config.OpPattern{{Method: "POST", Path: "[x"}} }, "reads[0]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := env("test_data")
			c.mutate(&e)
			_, err := effects.New(e)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("New = %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("New error = %v, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

func FuzzAdmit(f *testing.F) {
	for _, s := range []struct{ method, url string }{
		{"GET", "https://api.uat.example.com/v1/orders/1"},
		{"POST", "https://api.uat.example.com@evil.example/v1"},
		{"POST", "https://api.uat.example.com/v1/orders/../payouts/7/send"},
		{"get", "HTTP://API.UAT.EXAMPLE.COM.:80/v1"},
		{"GET", "http://127.0.0.1:8080/"},
		{"GET", "https://[::1]/"},
		{"GET", "https://api.uat.example.com%2eevil.example/"},
		{"GET", "https://api.uat.example.com\\@evil.example/"},
		{"POST", "https://api.uat.example.com/v1/payouts%2f7"},
		{"", ""},
	} {
		f.Add(s.method, s.url)
	}
	e := env("test_data")
	e.Hosts = []string{"api.uat.example.com", "127.0.0.1:8080"}
	p, err := effects.New(e)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, method, raw string) {
		u, err := url.Parse(raw)
		if err != nil {
			return
		}
		if p.Admit(effects.Request{Method: method, URL: u}) != nil {
			return
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("admitted scheme %q in %q", u.Scheme, raw)
		}
		host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		switch {
		case host == "api.uat.example.com" && (port == "80" && u.Scheme == "http" || port == "443" && u.Scheme == "https"):
		case host == "127.0.0.1" && port == "8080":
		default:
			t.Fatalf("admitted host %q port %q from %q", host, port, raw)
		}
	})
}
