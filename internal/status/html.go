package status

import (
	"fmt"
	"html/template"
	"io"
	"strings"
)

type htmlGroup struct {
	Name  string
	Count int
	Items []htmlItem
}

type htmlItem struct {
	ID, Title, Next string
	Reasons         []string
	Notes           []string
	Reads           []string // where the result of each test came from: the test, its word, and the run
	Check           *htmlCheck
}

type htmlCheck struct {
	Why, Expected string
	Steps         []string
}

type htmlPage struct {
	Lead, Count, Verdict string
	Models               string
	Ship                 bool
	Groups               []htmlGroup
	NotPlanned           []string
	NoTicket             []string
}

// HTML writes the same content as Text as one document that needs no network: the first line, the
// verdict, every group with every use case and the run its result was read from, and the manual
// checks with their steps. Every text is escaped, and the page holds no script and no outside
// resource.
func HTML(w io.Writer, r Report) error {
	p := htmlPage{Lead: line(r.Lead()), Count: line(r.Count()), Verdict: line(r.Verdict()), Models: line(r.ModelLine()), Ship: r.Ship(), NotPlanned: lines(r.NotPlanned), NoTicket: lines(r.NoTicket)}
	for _, g := range Groups {
		if r.Counts[g] == 0 {
			continue
		}
		hg := htmlGroup{Name: string(g), Count: r.Counts[g]}
		for _, it := range r.Items {
			if it.Group == g {
				hg.Items = append(hg.Items, htmlItemOf(it))
			}
		}
		p.Groups = append(p.Groups, hg)
	}
	if err := pageTemplate.Execute(w, p); err != nil {
		return fmt.Errorf("write the status page: %w", err)
	}
	return nil
}

func lines(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = line(s)
	}
	return out
}

func htmlItemOf(it Item) htmlItem {
	e := it.Entry
	hi := htmlItem{ID: line(e.Obligation.ID), Title: line(e.Obligation.Title), Next: it.Next, Notes: lines(it.Notes)}
	if it.Group != Verified {
		hi.Reasons = lines(withoutSentence(e))
	}
	for _, c := range e.Covers {
		if !c.Found {
			continue
		}
		subject := string(c.Scenario)
		if c.Journey != "" {
			subject = "journey " + c.Journey
		}
		word := c.Outcome
		if c.Word != "" {
			word = c.Word
		}
		hi.Reads = append(hi.Reads, line(fmt.Sprintf("%s: %s, read from run %s", subject, strings.ReplaceAll(word, "_", " "), c.Run)))
	}
	if m := e.Obligation.Manual; m != nil && it.Group == ForAPerson {
		hi.Check = &htmlCheck{Why: line(m.Text), Expected: line(m.Expected), Steps: lines(m.Steps)}
	}
	return hi
}

var pageTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Semar status</title>
<style>
:root { color-scheme: light dark; --ok: #1a7f37; --bad: #cf222e; --muted: #6e7781; --line: #d0d7de; }
@media (prefers-color-scheme: dark) { :root { --ok: #3fb950; --bad: #f85149; --muted: #8b949e; --line: #30363d; } }
body { font: 16px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 56rem; padding: 1.5rem 1rem; }
h1 { font-size: 1.2rem; margin: 0 0 .25rem; }
h2 { font-size: 1rem; margin: 1.5rem 0 .5rem; border-bottom: 1px solid var(--line); padding-bottom: .25rem; }
.count, .muted { color: var(--muted); }
.verdict { font-weight: 600; margin: 1rem 0; }
.verdict.yes { color: var(--ok); }
.verdict.no { color: var(--bad); }
ul { padding-left: 1.25rem; }
li { margin: .5rem 0; }
code { font: .9em ui-monospace, monospace; }
</style>
</head>
<body>
<h1>{{ .Lead }}</h1>
<p class="count">{{ .Count }}</p>
<p class="verdict {{ if .Ship }}yes{{ else }}no{{ end }}">{{ .Verdict }}</p>
{{ range .Groups }}
<section>
<h2>{{ .Name }} ({{ .Count }})</h2>
<ul>
{{ range .Items }}<li><strong>{{ .ID }}</strong> {{ .Title }}
{{ range .Reasons }}<div>{{ . }}</div>
{{ end }}{{ range .Notes }}<div class="muted">{{ . }}</div>
{{ end }}{{ range .Reads }}<div class="muted">{{ . }}</div>
{{ end }}{{ if .Check }}<div>Why a person: {{ .Check.Why }}</div>
<ol>{{ range .Check.Steps }}<li>{{ . }}</li>{{ end }}</ol>
<div>Expected: {{ .Check.Expected }}</div>
{{ end }}{{ if .Next }}<div>Next: <code>{{ .Next }}</code></div>
{{ end }}</li>
{{ end }}</ul>
</section>
{{ end }}{{ if .Models }}<p class="count">{{ .Models }}</p>
{{ end }}{{ if .NotPlanned }}<section>
<h2>not planned ({{ len .NotPlanned }})</h2>
<ul>{{ range .NotPlanned }}<li>{{ . }} <code>semar plan</code></li>{{ end }}</ul>
</section>
{{ end }}{{ if .NoTicket }}<section>
<h2>commits with no ticket</h2>
<ul>{{ range .NoTicket }}<li>{{ . }}</li>{{ end }}</ul>
</section>
{{ end }}</body>
</html>
`))
