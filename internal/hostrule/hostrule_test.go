package hostrule_test

import (
	"net/url"
	"testing"

	"github.com/jancuk/semar/internal/hostrule"
)

func TestAllowed(t *testing.T) {
	hosts := []string{"api.uat.example.com", "127.0.0.1:8080", "[::1]:9000", "::2", "Mixed.Example.COM."}
	cases := []struct {
		name, url string
		want      bool
	}{
		{"plain https", "https://api.uat.example.com/x", true},
		{"plain http", "http://api.uat.example.com/x", true},
		{"upper case", "https://API.UAT.example.com/x", true},
		{"trailing dot", "https://api.uat.example.com./x", true},
		{"entry normalized", "https://mixed.example.com/x", true},
		{"default port spelled", "https://api.uat.example.com:443/x", true},
		{"wrong default port", "https://api.uat.example.com:80/x", false},
		{"port entry", "http://127.0.0.1:8080/x", true},
		{"port entry without port", "http://127.0.0.1/x", false},
		{"port entry other port", "http://127.0.0.1:8081/x", false},
		{"ipv6 with port", "http://[::1]:9000/x", true},
		{"ipv6 wrong port", "http://[::1]/x", false},
		{"bare ipv6", "http://[::2]/x", true},
		{"user info", "https://api.uat.example.com@evil.example/", false},
		{"user info on listed host", "https://evil:pw@api.uat.example.com/x", true},
		{"suffix", "https://api.uat.example.com.evil.example/", false},
		{"prefix", "https://evil.api.uat.example.com/", false},
		{"ftp", "ftp://api.uat.example.com/", false},
		{"no scheme", "//api.uat.example.com/", false},
		{"opaque", "https:api.uat.example.com", false},
		{"empty host", "https:///x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := url.Parse(c.url)
			if err != nil {
				t.Fatal(err)
			}
			if got := hostrule.Allowed(u, hosts); got != c.want {
				t.Fatalf("Allowed(%s) = %v, want %v", c.url, got, c.want)
			}
		})
	}
	if hostrule.Allowed(nil, hosts) {
		t.Fatal("Allowed(nil) = true")
	}
}
