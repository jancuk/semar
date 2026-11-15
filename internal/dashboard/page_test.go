package dashboard

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/usage"
)

func checkPage(t *testing.T, page string) {
	t.Helper()
	for _, p := range pageProblems(page) {
		t.Error(p)
	}
}

// pageProblems parses the page and names an inline script, an inline style, an event
// attribute, and anything loaded from another host.
func pageProblems(page string) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	dec := xml.NewDecoder(strings.NewReader(page))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	var scripts, styles int
	inScript := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			add("parse the page: %v", err)
			return out
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(tok.Name.Local)
			attrs := map[string]string{}
			for _, a := range tok.Attr {
				attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			for k, v := range attrs {
				if k == "style" || strings.HasPrefix(k, "on") {
					add("<%s> has the inline attribute %s", name, k)
				}
				if k == "src" || k == "href" || k == "action" || k == "srcset" {
					if v != "data:," && (strings.Contains(v, "//") || strings.Contains(v, ":")) {
						add("<%s %s=%q> loads from another place", name, k, v)
					}
				}
			}
			switch name {
			case "style":
				add("the page has a <style> element")
			case "script":
				scripts++
				inScript = true
				if attrs["src"] != "app.js" {
					add("a script without src=app.js: %v", attrs)
				}
			case "link":
				if attrs["rel"] == "stylesheet" {
					styles++
					if attrs["href"] != "style.css" {
						add("a stylesheet other than style.css: %v", attrs)
					}
				}
			case "base", "iframe", "object", "embed", "form", "img":
				add("the page has a <%s> element", name)
			}
		case xml.EndElement:
			if strings.EqualFold(tok.Name.Local, "script") {
				inScript = false
			}
		case xml.CharData:
			if inScript && strings.TrimSpace(string(tok)) != "" {
				add("a script element holds inline code")
			}
		}
	}
	if scripts != 1 || styles != 1 {
		add("scripts %d, stylesheets %d", scripts, styles)
	}
	return out
}

func TestCheckPageFindsInlineCode(t *testing.T) {
	bad := []string{
		`<html><head><script>alert(1)</script><link rel="stylesheet" href="style.css"></head></html>`,
		`<html><head><script src="app.js"></script><link rel="stylesheet" href="style.css"></head><body><p style="color:red">x</p></body></html>`,
		`<html><head><script src="https://cdn.example.com/app.js"></script><link rel="stylesheet" href="style.css"></head></html>`,
		`<html><head><script src="app.js"></script><link rel="stylesheet" href="//cdn.example.com/a.css"></head></html>`,
		`<html><head><script src="app.js"></script><link rel="stylesheet" href="style.css"></head><body onload="x()"></body></html>`,
	}
	for _, page := range bad {
		if len(pageProblems(page)) == 0 {
			t.Errorf("the check passed %s", page)
		}
	}
}

func TestUsage(t *testing.T) {
	fx := newFixture(t)
	now := fx.s.d.Now()
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	recs := []usage.Record{
		{At: at(40 * 24 * time.Hour), Provider: "claude", Kind: "claude", Model: "", Outcome: "ok", Turns: 9, InputTokens: 900},
		{At: at(3 * 24 * time.Hour), Provider: "claude", Kind: "claude", Model: "", Outcome: "ok", Turns: 3, InputTokens: 100, OutputTokens: 10, CostUSD: 0.5,
			Limits: []provider.Limit{{Window: "five_hour", Utilization: 0.2, ResetsAt: now.Add(time.Hour), Status: "allowed"}}},
		{At: at(2 * time.Hour), Provider: "claude", Kind: "claude", Model: "", Outcome: "failed:usage_limit", Turns: 1, InputTokens: 50,
			Limits: []provider.Limit{{Window: "five_hour", Utilization: 0.95, ResetsAt: now.Add(time.Hour), Status: "rejected"}, {Window: "seven_day", Utilization: 0.4}}},
		{At: at(time.Hour), Provider: "deepseek", Kind: "openai", Model: "deepseek-chat", Outcome: "ok", Turns: 4, InputTokens: 400, OutputTokens: 40},
	}
	for _, r := range recs {
		if err := usage.Append(fx.home, r); err != nil {
			t.Fatal(err)
		}
	}
	var u usageBody
	a := fx.call("GET", "/api/usage?days=7", "")
	want(t, a, 200)
	a.json(t, &u)
	if u.Days != 7 || len(u.Rows) != 2 {
		t.Fatalf("usage %+v", u)
	}
	if r := u.Rows[0]; r.Provider != "claude" || r.Runs != 2 || r.Failed != 1 || r.Turns != 4 || r.InputTokens != 150 || r.CostUSD != 0.5 {
		t.Fatalf("claude row %+v", r)
	}
	if l := u.Limits["claude"]; len(l.Windows) != 2 || l.Windows[0].Utilization != 0.95 || !l.At.Equal(recs[2].At) {
		t.Fatalf("limits %+v", u.Limits)
	}
	if _, ok := u.Limits["deepseek"]; ok {
		t.Fatal("deepseek reported no limits")
	}
	if len(u.Recent) != 3 || u.Recent[0].Provider != "deepseek" || u.Recent[2].Turns != 3 {
		t.Fatalf("recent %+v", u.Recent)
	}
	if !strings.Contains(a.body, `"inputTokens":150`) || !strings.Contains(a.body, `"costUsd":0.5`) {
		t.Fatalf("field names: %s", a.body)
	}

	fx.call("GET", "/api/usage", "").json(t, &u)
	if u.Days != 30 || len(u.Recent) != 3 {
		t.Fatalf("default %+v", u)
	}
	fx.call("GET", "/api/usage?days=1", "").json(t, &u)
	if len(u.Recent) != 2 {
		t.Fatalf("today %+v", u.Recent)
	}
	fx.call("GET", "/api/usage?days=365", "").json(t, &u)
	if len(u.Recent) != 4 {
		t.Fatalf("a year %+v", u.Recent)
	}
	for _, q := range []string{"0", "366", "-1", "seven", "1.5"} {
		want(t, fx.call("GET", "/api/usage?days="+q, ""), 400)
	}
}

func TestUsageEmptyAndCapped(t *testing.T) {
	fx := newFixture(t)
	var u usageBody
	a := fx.call("GET", "/api/usage", "")
	want(t, a, 200)
	a.json(t, &u)
	if u.Rows == nil || u.Recent == nil || u.Limits == nil || len(u.Rows) != 0 {
		t.Fatalf("empty %s", a.body)
	}
	now := fx.s.d.Now()
	for i := range 60 {
		if err := usage.Append(fx.home, usage.Record{At: now.Add(-time.Duration(60-i) * time.Minute), Provider: "claude", Kind: "claude", Outcome: "ok", Turns: i}); err != nil {
			t.Fatal(err)
		}
	}
	fx.call("GET", "/api/usage", "").json(t, &u)
	if len(u.Recent) != 50 || u.Recent[0].Turns != 59 || u.Recent[49].Turns != 10 || u.Rows[0].Runs != 60 {
		t.Fatalf("recent %d, first %d", len(u.Recent), u.Recent[0].Turns)
	}
}
