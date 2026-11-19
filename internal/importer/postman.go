package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jancuk/semar/internal/redact"
)

var variableRef = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

type postmanCollection struct {
	Info struct {
		Schema string `json:"schema"`
	} `json:"info"`
	Item     []postmanItem `json:"item"`
	Variable []postmanVar  `json:"variable"`
}

type postmanVar struct {
	Key      string `json:"key"`
	ID       string `json:"id"`
	Value    any    `json:"value"`
	Disabled bool   `json:"disabled"`
}

func (v postmanVar) name() string {
	if v.Key != "" {
		return v.Key
	}
	return v.ID
}

type postmanItem struct {
	Name     string          `json:"name"`
	Item     []postmanItem   `json:"item"`
	Variable []postmanVar    `json:"variable"`
	Request  json.RawMessage `json:"request"`
	Response []struct {
		Code int `json:"code"`
	} `json:"response"`
	Event []struct {
		Listen string `json:"listen"`
		Script struct {
			Exec json.RawMessage `json:"exec"`
		} `json:"script"`
	} `json:"event"`
}

type postmanRequest struct {
	Method string          `json:"method"`
	URL    json.RawMessage `json:"url"`
	Header []struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Disabled bool   `json:"disabled"`
	} `json:"header"`
	Body *struct {
		Mode    string `json:"mode"`
		Raw     string `json:"raw"`
		Options struct {
			Raw struct {
				Language string `json:"language"`
			} `json:"raw"`
		} `json:"options"`
	} `json:"body"`
}

type postmanURL struct {
	Raw      string `json:"raw"`
	Protocol string `json:"protocol"`
	Host     any    `json:"host"`
	Path     any    `json:"path"`
	Port     string `json:"port"`
	Query    []struct {
		Key      string `json:"key"`
		Value    string `json:"value"`
		Disabled bool   `json:"disabled"`
	} `json:"query"`
}

func readPostman(data []byte, drop func(string) bool) (File, error) {
	var c postmanCollection
	if err := json.Unmarshal(data, &c); err != nil {
		return File{}, ErrNotSupported
	}
	if !strings.Contains(c.Info.Schema, "/collection/v2.1") {
		return File{}, errors.New("the Postman collection is not format 2.1; export it again as Collection v2.1")
	}
	f := File{Format: Postman}
	scope := map[string]string{}
	addVars(scope, c.Variable)
	walkPostman(c.Item, scope, "", drop, &f)
	return f, nil
}

func addVars(scope map[string]string, vars []postmanVar) {
	for _, v := range vars {
		if v.Disabled || v.name() == "" {
			continue
		}
		switch x := v.Value.(type) {
		case nil:
			scope[v.name()] = ""
		case string:
			scope[v.name()] = x
		default:
			scope[v.name()] = fmt.Sprint(x)
		}
	}
}

func walkPostman(items []postmanItem, parent map[string]string, folder string, drop func(string) bool, f *File) {
	for _, it := range items {
		name := it.Name
		if folder != "" {
			name = folder + " / " + it.Name
		}
		if len(it.Item) > 0 {
			scope := make(map[string]string, len(parent))
			for k, v := range parent {
				scope[k] = v
			}
			addVars(scope, it.Variable)
			walkPostman(it.Item, scope, name, drop, f)
			continue
		}
		if len(it.Request) == 0 {
			continue
		}
		if hasTest(it) {
			f.WithTests++
		}
		imp, why := postmanRequestOf(name, it, parent, drop)
		if why != "" {
			f.Left = append(f.Left, Left{Name: name, Reason: why})
			continue
		}
		f.Requests = append(f.Requests, imp)
	}
}

func hasTest(it postmanItem) bool {
	for _, e := range it.Event {
		if e.Listen == "test" && len(bytes.TrimSpace(e.Script.Exec)) > 2 && string(bytes.TrimSpace(e.Script.Exec)) != "null" {
			return true
		}
	}
	return false
}

// postmanRequestOf reads one item. A header that the redaction rules or the shape of a secret name is
// never substituted, so that a variable that only a dropped header uses needs no value.
func postmanRequestOf(name string, it postmanItem, vars map[string]string, drop func(string) bool) (Imported, string) {
	var raw postmanRequest
	if len(it.Request) > 0 && it.Request[0] == '"' {
		var s string
		if err := json.Unmarshal(it.Request, &s); err != nil {
			return Imported{}, "the request cannot be read"
		}
		raw.Method, raw.URL = "GET", it.Request
	} else if err := json.Unmarshal(it.Request, &raw); err != nil {
		return Imported{}, "the request cannot be read"
	}
	imp := Imported{Name: name, Method: strings.ToUpper(raw.Method)}
	if imp.Method == "" {
		imp.Method = "GET"
	}
	for _, e := range it.Response {
		if e.Code > 0 {
			imp.Status = e.Code
			break
		}
	}

	var secretVar string
	var headerSecret bool
	sub := func(text string, inHeader bool) (string, string) {
		var missing string
		headerSecret = false
		out := variableRef.ReplaceAllStringFunc(text, func(m string) string {
			n := variableRef.FindStringSubmatch(m)[1]
			v, ok := vars[n]
			if !ok || v == "" {
				if missing == "" {
					missing = n
				}
				return m
			}
			if redact.Name(n) {
				if inHeader {
					headerSecret = true
				} else if secretVar == "" {
					secretVar = n
				}
			}
			return v
		})
		if missing != "" {
			return "", fmt.Sprintf("the variable {{%s}} has no value in the collection", missing)
		}
		return out, ""
	}

	rawURL, why := postmanURLText(raw.URL)
	if why != "" {
		return Imported{}, why
	}
	u, why := sub(rawURL, false)
	if why != "" {
		return Imported{}, why
	}
	imp.URL = u

	for _, h := range raw.Header {
		if h.Disabled || h.Key == "" {
			continue
		}
		if drop(h.Key) {
			continue
		}
		v, why := sub(h.Value, true)
		if headerSecret {
			continue
		}
		if why != "" {
			return Imported{}, why
		}
		imp.Headers = append(imp.Headers, Header{h.Key, v})
	}

	if b := raw.Body; b != nil && b.Mode != "" {
		switch b.Mode {
		case "raw":
			text, why := sub(b.Raw, false)
			if why != "" {
				return Imported{}, why
			}
			if strings.TrimSpace(text) != "" {
				imp.Body = []byte(text)
			}
		default:
			return Imported{}, "the body is not JSON"
		}
	}
	if secretVar != "" {
		return Imported{}, fmt.Sprintf("the variable {{%s}} looks like a secret, and Semar copies no secret", secretVar)
	}
	return imp, ""
}

func postmanURLText(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", "the request has no URL"
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || s == "" {
			return "", "the request has no URL"
		}
		return s, ""
	}
	var u postmanURL
	if err := json.Unmarshal(raw, &u); err != nil {
		return "", "the request has no URL"
	}
	if u.Raw != "" {
		return u.Raw, ""
	}
	host, path := joinParts(u.Host, "."), joinParts(u.Path, "/")
	if host == "" {
		return "", "the request has no URL"
	}
	var b strings.Builder
	if u.Protocol != "" {
		b.WriteString(u.Protocol + "://")
	}
	b.WriteString(host)
	if u.Port != "" {
		b.WriteString(":" + u.Port)
	}
	b.WriteString("/" + path)
	sep := "?"
	for _, q := range u.Query {
		if q.Disabled || q.Key == "" {
			continue
		}
		b.WriteString(sep + url.QueryEscape(q.Key) + "=" + url.QueryEscape(q.Value))
		sep = "&"
	}
	return b.String(), ""
}

func joinParts(v any, sep string) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, p := range x {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, sep)
	}
	return ""
}
