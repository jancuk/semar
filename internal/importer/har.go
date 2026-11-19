package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"strings"
)

type harFile struct {
	Log struct {
		Version string `json:"version"`
		Entries []struct {
			Request struct {
				Method   string      `json:"method"`
				URL      string      `json:"url"`
				Headers  []harHeader `json:"headers"`
				PostData *struct {
					MimeType string `json:"mimeType"`
					Text     string `json:"text"`
					Params   []any  `json:"params"`
				} `json:"postData"`
			} `json:"request"`
			Response struct {
				Status  int         `json:"status"`
				Headers []harHeader `json:"headers"`
				Content struct {
					MimeType string `json:"mimeType"`
				} `json:"content"`
			} `json:"response"`
		} `json:"entries"`
	} `json:"log"`
}

type harHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func readHAR(data []byte, drop func(string) bool) (File, error) {
	var h harFile
	if err := json.Unmarshal(data, &h); err != nil {
		return File{}, ErrNotSupported
	}
	f := File{Format: HAR}
	seen := map[string]bool{}
	for _, e := range h.Log.Entries {
		req := e.Request
		name := strings.ToUpper(req.Method) + " " + addressOnly(req.URL)
		if !isJSONType(e.Response.Content.MimeType) && !isJSONType(headerValue(e.Response.Headers, "Content-Type")) {
			continue
		}
		imp := Imported{Name: name, Method: strings.ToUpper(req.Method), URL: req.URL, Status: e.Response.Status}
		for _, hd := range req.Headers {
			if hd.Name == "" || strings.HasPrefix(hd.Name, ":") || drop(hd.Name) {
				continue
			}
			imp.Headers = append(imp.Headers, Header{hd.Name, hd.Value})
		}
		if pd := req.PostData; pd != nil && (pd.Text != "" || len(pd.Params) > 0) {
			if pd.Text == "" {
				f.Left = append(f.Left, Left{name, "the body is not JSON"})
				continue
			}
			imp.Body = []byte(pd.Text)
		}
		key := imp.Method + " " + imp.URL + " " + digest(imp.Body)
		if seen[key] {
			continue
		}
		seen[key] = true
		f.Requests = append(f.Requests, imp)
	}
	if h.Log.Version != "" && !strings.HasPrefix(h.Log.Version, "1.") {
		return File{}, fmt.Errorf("the HAR file is version %s, and semar import reads version 1.2", h.Log.Version)
	}
	return f, nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func headerValue(hs []harHeader, name string) string {
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func isJSONType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// addressOnly is the URL with no user info, no query, and no fragment, for a name that is printed.
func addressOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(a request with no address)"
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}
