package web

import (
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
)

func TestOnlyAHostThatIsNotListedIsABlockedHost(t *testing.T) {
	policy, err := effects.New(config.Environment{
		Hosts:   []string{"shop.example.com"},
		Methods: []string{"GET", "POST"},
		Effects: config.Effects{Mode: "read_only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(policy, func() string { return "0000000000000000" })
	cases := []struct {
		name, method, url string
		host              string
		refused           bool
	}{
		{"a host that is not listed", "GET", "https://cdn.example.com/a.js", "cdn.example.com", true},
		{"with a port", "GET", "http://127.0.0.1:41000/a", "127.0.0.1:41000", true},
		{"the default port of the scheme", "GET", "https://CDN.example.com:443/a", "cdn.example.com", true},
		{"a listed host with a write on a read only environment", "POST", "https://shop.example.com/orders", "", true},
		{"a listed host", "GET", "https://shop.example.com/orders", "", false},
		{"a data URL", "GET", "data:text/plain,hi", "", false},
		{"an address that does not parse", "GET", "http://[::1", "", true},
	}
	for _, c := range cases {
		host, refused := g.requestVerdict(c.method, c.url)
		if host != c.host || (refused != "") != c.refused {
			t.Errorf("%s: host %q, refused %q; want host %q, refused %v", c.name, host, refused, c.host, c.refused)
		}
	}
}
