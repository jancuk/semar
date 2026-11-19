// Package pagelist is what a model sees of a web page: a short list of the elements that a person can
// act on or read, each with a reference. The reference is the only way for a model to name an element,
// so a model cannot invent a locator. The package holds the shape of the list and its limits, and
// nothing that reads a browser.
package pagelist

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLines is the most elements that a list holds.
	MaxLines = 150
	// MaxOptions is the most option labels of a select list that the list shows.
	MaxOptions = 12
	// MaxText is the most characters of a name or a value. A longer text is cut and ends with an ellipsis.
	MaxText = 80
)

// Item is one element as the page reader found it, before the list is chosen. Index is the place of
// the element in the reader's own array of the page, which the browser needs to act on it. Acts
// says that a person can click, type into, or drag the element, or that it is a heading: such an
// element is kept when the page holds more than MaxLines.
type Item struct {
	Index    int    `json:"index"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	State    string `json:"state"` // checked or unchecked, for a box that has one
	Disabled bool   `json:"disabled"`
	Secret   bool   `json:"secret"` // a password field: its value is never read into the list
	Acts     bool   `json:"acts"`
	// Options are the labels of the options of a select list, up to MaxOptions.
	Options []string `json:"options,omitempty"`
}

// Element is one line of a list.
type Element struct {
	Ref      string // e<n>, counted from 1 in the order of the page
	Index    int    // see Item
	Role     string
	Name     string
	Value    string
	State    string
	Disabled bool
	Options  []string
}

// List is the page as a model sees it.
type List struct {
	URL      string // the address without its query and its fragment
	Title    string
	Elements []Element
	// Omitted is how many elements the page had beyond MaxLines.
	Omitted int
}

// Build chooses at most MaxLines items, the ones that act first and then the others, each group in the
// order of the page, and lists them in the order of the page. A cut item leaves a count behind.
func Build(rawURL, title string, items []Item) List {
	l := List{URL: withoutQuery(rawURL), Title: Cut(title)}
	keep := make([]bool, len(items))
	n := 0
	for _, acts := range []bool{true, false} {
		for i, it := range items {
			if it.Acts == acts && n < MaxLines {
				keep[i] = true
				n++
			}
		}
	}
	l.Omitted = len(items) - n
	for i, it := range items {
		if !keep[i] {
			continue
		}
		e := Element{
			Ref: "e" + strconv.Itoa(len(l.Elements)+1), Index: it.Index, Role: roleOf(it.Role), Name: Cut(it.Name),
			Value: Cut(it.Value), State: it.State, Disabled: it.Disabled,
		}
		for _, o := range it.Options[:min(len(it.Options), MaxOptions)] {
			e.Options = append(e.Options, Cut(o))
		}
		if it.Secret {
			e.Value = ""
		}
		l.Elements = append(l.Elements, e)
	}
	return l
}

// roleOf keeps a role that a page wrote to lower-case letters and hyphens, so that it cannot add a
// line or a quote to the list.
func roleOf(role string) string {
	role = strings.ToLower(role)
	if role == "" || strings.Trim(role, "abcdefghijklmnopqrstuvwxyz-") != "" || len(role) > 24 {
		return "generic"
	}
	return role
}

// Cut limits a text to MaxText characters and puts the spaces of it on one line.
func Cut(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= MaxText {
		return s
	}
	return string([]rune(s)[:MaxText-1]) + "…"
}

func withoutQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String()
}

// Find gives the element of a reference.
func (l List) Find(ref string) (Element, bool) {
	for _, e := range l.Elements {
		if e.Ref == ref {
			return e, true
		}
	}
	return Element{}, false
}

// valueForms are the roles of the fields that the browser draws with a picker, and the form that their
// value takes, which is the only text that a fill of them can hold.
var valueForms = map[string]string{
	"date": "YYYY-MM-DD", "datetime-local": "YYYY-MM-DDTHH:MM", "time": "HH:MM", "month": "YYYY-MM", "week": "YYYY-Www",
}

// String is the list as the prompt shows it: the address, the title, and a line for each element.
func (l List) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Page: %s\nTitle: %s\n", l.URL, l.Title)
	for _, e := range l.Elements {
		fmt.Fprintf(&b, "%s %s %s", e.Ref, e.Role, strconv.Quote(e.Name))
		if e.Value != "" {
			fmt.Fprintf(&b, " value=%s", strconv.Quote(e.Value))
		}
		if len(e.Options) > 0 {
			quoted := make([]string, len(e.Options))
			for i, o := range e.Options {
				quoted[i] = strconv.Quote(o)
			}
			b.WriteString(" options=[" + strings.Join(quoted, ", ") + "]")
		}
		if form, ok := valueForms[e.Role]; ok {
			b.WriteString(" form=" + form)
		}
		if e.State != "" {
			b.WriteString(" " + e.State)
		}
		if e.Disabled {
			b.WriteString(" disabled")
		}
		b.WriteByte('\n')
	}
	if l.Omitted > 0 {
		fmt.Fprintf(&b, "%d more elements are not shown.\n", l.Omitted)
	}
	return b.String()
}
