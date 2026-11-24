package mobilemcp

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/effects"
)

const maxURL = 2048

// deniedSchemes can never be opened by a tool, whatever is listed or opted into. They are the denied
// schemes of the engine except http and https.
var deniedSchemes = []string{"file", "content", "intent", "javascript", "data", "about", "vbscript"}

// UnsafeURLsEnv is the environment variable that allows schemes beyond http and https.
const UnsafeURLsEnv = "SEMAR_MCP_ALLOW_UNSAFE_URLS"

// URLGate decides which links mobile_open_url may open.
type URLGate struct {
	// Policy is the effects policy of the bound environment. An http or https link is admitted as a GET.
	// Nil in standalone mode, where http and https are allowed.
	Policy *effects.Policy
	// AllowUnsafe allows any scheme that is not always refused: --allow-unsafe-urls or SEMAR_MCP_ALLOW_UNSAFE_URLS=1.
	AllowUnsafe bool
	// Schemes are the schemes of android.schemes in the services of the repository.
	Schemes []string
}

// AllowUnsafeFromEnv reads UnsafeURLsEnv: only the value 1 opts in.
func AllowUnsafeFromEnv(lookup func(string) (string, bool)) bool {
	v, ok := lookup(UnsafeURLsEnv)
	return ok && strings.TrimSpace(v) == "1"
}

// Check returns nil when the link may be opened, and an error that says why not.
func (g URLGate) Check(link string) error {
	switch {
	case link == "":
		return errors.New("the link is empty")
	case len(link) > maxURL:
		return fmt.Errorf("the link is longer than %d bytes", maxURL)
	case strings.ContainsFunc(link, func(r rune) bool { return r < 0x20 || r == 0x7f || r == ' ' }):
		return errors.New("the link holds a space or a control character")
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" {
		return errors.New("the link is not a URL with a scheme")
	}
	scheme := strings.ToLower(u.Scheme)
	if slices.Contains(deniedSchemes, scheme) {
		return fmt.Errorf("the scheme %s can never be opened", scheme)
	}
	if scheme == "http" || scheme == "https" {
		if g.Policy == nil {
			return nil
		}
		if err := g.Policy.Admit(effects.Request{Method: "GET", URL: u}); err != nil {
			return errors.New(refusalText(err, "open"))
		}
		return nil
	}
	if g.AllowUnsafe || slices.Contains(g.Schemes, scheme) {
		return nil
	}
	return fmt.Errorf("the scheme %s is not allowed: list it under android.schemes, or start the server with --allow-unsafe-urls (or %s=1)", scheme, UnsafeURLsEnv)
}

// refusalText keeps the kind and the fixed reason of a policy refusal. The policy quotes the path of the
// request in some of its reasons, and the path can hold a value.
func refusalText(err error, label string) string {
	var r *effects.Refusal
	if !errors.As(err, &r) {
		return "the effects policy refused " + label
	}
	switch r.Kind {
	case effects.HostNotAllowed:
		return r.Error()
	case effects.ReadOnly:
		return string(r.Kind) + ": " + label + " writes, and the environment is read only"
	case effects.MethodNotAllowed:
		return string(r.Kind) + ": the method of " + label + " is not in the environment methods"
	default:
		return string(r.Kind) + ": " + label + " is not allowed: its path is not normal or it matches the deny list"
	}
}
