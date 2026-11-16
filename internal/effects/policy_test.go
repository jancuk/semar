package effects_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
)

func env(mode string) config.Environment {
	return config.Environment{
		Hosts:   []string{"api.uat.example.com"},
		Methods: []string{"GET", "POST", "DELETE"},
		Effects: config.Effects{
			Mode:  mode,
			Deny:  []config.OpPattern{{Method: "POST", Path: "/v1/payouts/**"}, {Method: "POST", Path: "/v1/payouts"}, {Method: "POST", Path: "/V1/Refunds/"}},
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
		{"deny trailing slash", "test_data", "POST", h + "/v1/payouts/", "denied"},
		{"deny pattern upper case and trailing slash", "test_data", "POST", h + "/v1/refunds", "denied"},
		{"deny upper case", "test_data", "POST", h + "/v1/Payouts/7/send", "denied"},
		{"deny segment parameter", "test_data", "POST", h + "/v1/payouts;x=1/7/send", "denied"},
		{"deny parameter on last segment", "test_data", "POST", h + "/v1/payouts/7;x=1", "denied"},
		{"read only search trailing slash", "read_only", "POST", h + "/v1/search/", "read_only"},
		{"read only search upper case", "read_only", "POST", h + "/v1/Search", "read_only"},
		{"non-ascii host dotted I", "test_data", "GET", "https://ap\u0130.uat.example.com/v1/orders", "host_not_allowed"},
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
		{"reads pattern trailing slash", "POST", h + "/v1/search/", effects.Write},
		{"reads pattern upper case", "POST", h + "/v1/Search", effects.Write},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Classify(req(t, c.method, c.url)); got != c.want {
				t.Fatalf("Classify = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDenied(t *testing.T) {
	p, err := effects.New(env("test_data"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, url string
		want              bool
	}{
		{"match", "POST", "https://other.example.net/v1/payouts/x/send", true},
		{"lower case method", "post", "/v1/payouts/x/send", true},
		{"trailing slash", "POST", "/v1/payouts/", true},
		{"upper case", "POST", "/v1/Payouts/x", true},
		{"wrong method", "GET", "/v1/payouts/x/send", false},
		{"other path", "POST", "/v1/orders", false},
		{"allowed in test data", "POST", "/v1/vouchers/9/redeem", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.Denied(req(t, c.method, c.url)); got != c.want {
				t.Fatalf("Denied = %v, want %v", got, c.want)
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
		{"trailing slash", "POST", h + "/v1/vouchers/9/redeem/", true},
		{"upper case", "POST", h + "/v1/Vouchers/9/Redeem", true},
		{"segment parameter", "POST", h + "/v1/vouchers;x=1/9/redeem;y=2", true},
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
		{"host entry with colon and no port", func(e *config.Environment) { e.Hosts = []string{"ok.example.com", "api.uat.example.com:"} }, "api.uat.example.com:"},
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
		{"GET", "https://ap\u0130.uat.example.com/v1/orders"},
		{"GET", "https://\u212A.uat.example.com/v1/orders"},
		{"", ""},
	} {
		f.Add(s.method, s.url)
	}
	e := env("test_data")
	e.Hosts = []string{"api.uat.example.com", "127.0.0.1:8080", "k.uat.example.com"}
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
		host := u.Hostname()
		var lower []byte
		for i := 0; i < len(host); i++ {
			b := host[i]
			if b > 0x7f {
				t.Fatalf("admitted a host with a non-ASCII byte: %q from %q", host, raw)
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			lower = append(lower, b)
		}
		want := strings.TrimSuffix(string(lower), ".")
		for _, entry := range e.Hosts {
			entryHost, _, _ := strings.Cut(entry, ":")
			if entryHost == want {
				return
			}
		}
		t.Fatalf("admitted host %q (from %q) matches no entry of the hosts list", want, raw)
	})
}

func slashEnv(deny, once string) config.Environment {
	e := env("test_data")
	e.Effects.Deny = []config.OpPattern{{Method: "POST", Path: deny}}
	e.Effects.Once = []config.OpPattern{{Method: "POST", Path: once}}
	return e
}

func TestAdmitDenyTrailingSlashForms(t *testing.T) {
	const h = "https://api.uat.example.com"
	cases := []struct{ name, deny, url, want string }{
		{"star slash", "/v1/payouts/*", h + "/v1/payouts/", "denied"},
		{"star child", "/v1/payouts/*", h + "/v1/payouts/7", "denied"},
		{"star upper child slash", "/v1/payouts/*", h + "/v1/Payouts/7/", "denied"},
		{"exact slash", "/v1/payouts", h + "/v1/payouts/", "denied"},
		{"exact", "/v1/payouts", h + "/v1/payouts", "denied"},
		{"globstar slash", "/v1/payouts/**", h + "/v1/payouts/", "denied"},
		{"globstar deep", "/v1/payouts/**", h + "/v1/payouts/7/send", "denied"},
		{"globstar deep slash", "/v1/payouts/**", h + "/v1/payouts/7/send/", "denied"},
		{"uncovered path admitted", "/v1/payouts/*", h + "/v1/orders/", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := effects.New(slashEnv(c.deny, "/v1/unused"))
			if err != nil {
				t.Fatal(err)
			}
			err = p.Admit(req(t, "POST", c.url))
			if c.want == "" {
				if err != nil {
					t.Fatalf("Admit = %v, want nil", err)
				}
				return
			}
			var ref *effects.Refusal
			if !errors.As(err, &ref) || string(ref.Kind) != c.want {
				t.Fatalf("Admit = %v, want %s", err, c.want)
			}
		})
	}
}

func TestOnceTrailingSlashForms(t *testing.T) {
	p, err := effects.New(slashEnv("/v1/unused", "/v1/x/*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://api.uat.example.com/v1/x/", "https://api.uat.example.com/v1/x/9"} {
		if !p.Once(req(t, "POST", u)) {
			t.Fatalf("Once(%s) = false, want true", u)
		}
	}
}

// Generous matching must never admit what plain matching refused.
func TestDenyNeverLooserThanPlainMatch(t *testing.T) {
	patterns := []string{
		"/v1/payouts",
		"/v1/payouts/*",
		"/v1/payouts/**",
		"/v1/*/send",
		"/V1/Refunds",
		"/v1/refunds/",
		"/v1/a/*/b/",
	}
	for _, pat := range patterns {
		p, err := effects.New(slashEnv(pat, "/v1/unused"))
		if err != nil {
			t.Fatal(err)
		}
		base := strings.NewReplacer("**", "7/send", "*", "7").Replace(pat)
		empty := strings.NewReplacer("**", "", "*", "").Replace(pat)
		trimmed := strings.TrimSuffix(base, "/")
		for _, path := range []string{
			base, base + "/", trimmed, trimmed + "/", strings.ToUpper(base), strings.ToUpper(trimmed) + "/",
			withParam(trimmed), empty, strings.TrimSuffix(empty, "/"), strings.ToUpper(empty),
		} {
			ok, err := doublestar.Match(pat, path)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				continue
			}
			if err := p.Admit(req(t, "POST", "https://api.uat.example.com"+path)); err == nil {
				t.Errorf("pattern %q matches plain path %q but Admit admitted it", pat, path)
			}
		}
	}
}

func withParam(path string) string { return path + ";x=1" }
