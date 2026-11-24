package mobilerec

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/xpathlite"
)

// Purpose is what a locator is for. It decides which node is the target, and what a locator has to find.
type Purpose int

const (
	// ForTap targets the nearest clickable node: a row of Compose or React Native has its text in a leaf
	// and its click on a parent.
	ForTap Purpose = iota
	// ForType targets the field that takes the text.
	ForType
	// ForAssert targets the leaf: the node that shows what is checked.
	ForAssert
)

// Stability says how likely a locator is to survive a change of the app.
const (
	Strong = "strong"
	Medium = "medium"
	Weak   = "weak"
)

// The tiers of a locator, in the order of the ranking.
const (
	TierID     = "id"
	TierDesc   = "desc"
	TierRole   = "role"
	TierText   = "text"
	TierWithin = "within"
	TierXPath  = "xpath"
	// TierIndex is a position among the matches of another locator. It is never chosen on its own.
	TierIndex = "index"
)

const (
	probeName  = "target"
	probeInner = "container"
	// probeTo and probeToInner name the element that a drag drops on, and its container.
	probeTo      = "to"
	probeToInner = "toContainer"
	maxString    = 200
	// maxClimb is how many levels above the target a container is looked for.
	maxClimb = 5
	// maxLabels is how many labels of one ancestor are tried as the text that finds it.
	maxLabels = 5
	// maxLabel is the longest label that finds a container.
	maxLabel = 60
)

// Container is the element that a within locator is searched in. It is saved as an element of the screen.
type Container struct {
	Hint    string
	Locator domain.Locator
}

// Candidate is one locator that finds the target, as a replay would find it.
type Candidate struct {
	Locator domain.Locator
	Tier    string
	// Stability is strong for an id, medium for a description, a role with a name, and a text, and weak
	// for a container, an xpath, and text that changes between runs.
	Stability string
	// Container is set for the tier within: Locator.Within then holds probeInner until the builder names
	// the container.
	Container *Container
	// Dynamic is true when the string that the locator is made of looks like a date, an amount, or a number.
	Dynamic bool
	// Matches is how many visible elements the locator finds alone, for a locator that is ambiguous, and
	// the number of elements that an index counts in.
	Matches int
	// Explicit is true for a locator that Semar offers but never picks: a position. A person has to choose it.
	Explicit bool
	// Note says in plain words what is special about the locator, such as "by position 2 of 3".
	Note string
}

var (
	dynamicNumber   = regexp.MustCompile(`^[\s\p{Sc}+\-−]*\d[\d\s.,:%/]*$`)
	dynamicDigits   = regexp.MustCompile(`\d{4,}`)
	dynamicDate     = regexp.MustCompile(`\d{1,4}[-/.]\d{1,2}[-/.]\d{1,4}|\d{1,2}:\d{2}`)
	dynamicMonth    = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+\d{1,2}\b|\b\d{1,2}\s+(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)`)
	dynamicMoney    = regexp.MustCompile(`[\p{Sc}]\s*\d|\d\s*[\p{Sc}]|\bRp\.?\s*\d|\d+[.,]\d{2}\b`)
	dynamicHexID    = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-|\b[0-9a-f]{12,}\b`)
	dynamicRunWord  = regexp.MustCompile(`(?i)\brun[-_ ]?\d`)
	dynamicDuration = regexp.MustCompile(`\b\d+[smhd]\b`)
)

// IsDynamic reports whether a text looks like something that changes from one run to the next: a date, an
// amount, a number, an identifier.
func IsDynamic(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return dynamicNumber.MatchString(s) || dynamicDigits.MatchString(s) || dynamicDate.MatchString(s) ||
		dynamicMonth.MatchString(s) || dynamicMoney.MatchString(s) || dynamicHexID.MatchString(s) ||
		dynamicRunWord.MatchString(s) || dynamicDuration.MatchString(s)
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// usable says whether a string may be part of a saved locator.
func usable(ov *Overlay, s string) bool {
	switch {
	case s == "" || len([]rune(s)) > maxString:
		return false
	case strings.Contains(s, "{{"):
		return false
	case strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) }):
		return false
	case ov != nil && ov.Leaks(s):
		return false
	}
	return true
}

func shortID(id string) string {
	if _, short, ok := strings.Cut(id, ":id/"); ok {
		return short
	}
	return id
}

// Locate gives the locators that find the target, best first. The tiers are id, desc, role with name,
// text, within, and xpath. A locator is kept only when mobile.Resolve, which a replay uses, finds the
// target with it: exactly one visible match, and the node that the purpose asks for. An editable field is
// never found by its own text, which holds what was typed: its tiers are id, desc, and xpath on @hint.
//
// target is the node that the purpose names, already: for ForTap the nearest clickable node.
func Locate(t *mobile.Tree, target *mobile.Node, p Purpose, ov *Overlay) []Candidate {
	all, _ := LocateAll(t, target, p, ov)
	var out []Candidate
	for _, c := range all {
		if !c.Explicit {
			out = append(out, c)
		}
	}
	return out
}

// LocateAll gives what Locate gives, and more. found is the list of Locate, and ends with a position
// (Explicit) when no other locator finds the target alone: the first locator that matches several
// elements, with an index. ambiguous lists the locators that find the target among several visible
// elements, with how many, in the order of the tiers. A replay refuses those, and so does the recorder.
func LocateAll(t *mobile.Tree, target *mobile.Node, p Purpose, ov *Overlay) (found, ambiguous []Candidate) {
	if t == nil || target == nil {
		return nil, nil
	}
	editable := Editable(target)
	var out []Candidate
	seen := map[domain.Locator]bool{}
	tried := map[domain.Locator]bool{}
	// positions[i] is where the target is among the matches of ambiguous[i].
	var positions []int
	keep := func(c Candidate) {
		if seen[c.Locator] || !c.Locator.ForMobile() {
			return
		}
		if !confirms(t, c, target, p) {
			if c.Container == nil && !tried[c.Locator] {
				tried[c.Locator] = true
				if matches, pos := ambiguity(t, c.Locator, target, p); pos > 0 {
					c.Matches = matches
					ambiguous = append(ambiguous, c)
					positions = append(positions, pos)
				}
			}
			return
		}
		seen[c.Locator] = true
		out = append(out, c)
	}

	if target.ID != "" && usable(ov, target.ID) {
		short := shortID(target.ID)
		if short != target.ID && usable(ov, short) {
			keep(Candidate{Locator: domain.Locator{ID: short}, Tier: TierID, Stability: Strong})
		}
		keep(Candidate{Locator: domain.Locator{ID: target.ID}, Tier: TierID, Stability: Strong})
	}
	if d := collapseSpace(target.Desc); usable(ov, d) {
		keep(Candidate{Locator: domain.Locator{Desc: d}, Tier: TierDesc, Stability: stabilityOf(d), Dynamic: IsDynamic(d)})
	}
	name := target.AccName()
	if !editable {
		if target.Role != "" && usable(ov, name) {
			keep(Candidate{Locator: domain.Locator{Role: target.Role, Name: name}, Tier: TierRole, Stability: stabilityOf(name), Dynamic: IsDynamic(name)})
		}
		if usable(ov, target.Text) {
			keep(Candidate{Locator: domain.Locator{Text: target.Text}, Tier: TierText, Stability: stabilityOf(target.Text), Dynamic: IsDynamic(target.Text)})
		}
		// When a clickable Compose card merges a ticking badge/timestamp (e.g. "item 1s Delete Pin") into
		// its own text, a non-clickable child leaf often holds the stable label alone. Try those leaves so
		// rankDynamic prefers the stable label while confirms/tapAccepts still guarantees the tap hits target.
		if p == ForTap && IsDynamic(target.Text) {
			for _, leaf := range tapLeaves(target) {
				ln := leaf.AccName()
				if leaf.Role != "" && usable(ov, ln) && !IsDynamic(ln) {
					keep(Candidate{Locator: domain.Locator{Role: leaf.Role, Name: ln}, Tier: TierRole, Stability: Medium})
				}
				if usable(ov, leaf.Text) && !IsDynamic(leaf.Text) {
					keep(Candidate{Locator: domain.Locator{Text: leaf.Text}, Tier: TierText, Stability: Medium})
				}
			}
		}
		for _, c := range withinCandidates(t, target, p, ov) {
			keep(c)
		}
	}
	for _, x := range xpaths(target, editable, ov) {
		keep(Candidate{Locator: domain.Locator{XPath: x}, Tier: TierXPath, Stability: Weak})
	}
	// Jetpack Compose text fields often carry no resource-id, content-desc, or XML hint (their label is
	// a separate child TextView). Fall back to password-scoped xpath, unique class, or role+name when
	// multiple unlabeled fields share a screen so Compose forms stay recordable.
	if editable && len(out) == 0 && target.Class != "" && !strings.ContainsAny(target.Class, "[]'\" ") {
		if target.Password {
			x := "//" + target.Class + "[@password='true']"
			if _, err := xpathlite.Compile(x); err == nil {
				keep(Candidate{Locator: domain.Locator{XPath: x}, Tier: TierXPath, Stability: Weak})
			}
		}
		keep(Candidate{Locator: domain.Locator{Class: target.Class}, Tier: TierXPath, Stability: Weak})
		if len(out) == 0 && !target.Password && target.Role != "" && usable(ov, name) {
			keep(Candidate{Locator: domain.Locator{Role: target.Role, Name: name}, Tier: TierRole, Stability: Weak, Dynamic: IsDynamic(name)})
		}
	}
	out = rankDynamic(out)
	if len(out) == 0 {
		if c, ok := byPosition(t, ambiguous, positions, target, p); ok {
			out = append(out, c)
		}
	}
	return out, ambiguous
}

// ambiguity says how many visible elements the locator finds, and the position of the one that the
// purpose accepts as the target among them, counted from 1. The position is 0 when the locator finds
// fewer than two elements or not the target.
func ambiguity(t *mobile.Tree, loc domain.Locator, target *mobile.Node, p Purpose) (matches, pos int) {
	nodes, err := mobile.Find(t, loc, nil)
	if err != nil {
		return 0, 0
	}
	var visible []*mobile.Node
	for _, n := range nodes {
		if n.Visible {
			visible = append(visible, n)
		}
	}
	if len(visible) < 2 {
		return 0, 0
	}
	for i, n := range visible {
		if (p == ForTap && tapAccepts(target, n)) || n == target {
			return len(visible), i + 1
		}
	}
	return len(visible), 0
}

// byPosition makes the position candidate from the first ambiguous locator that an index makes unique.
// The locators that do not change between runs come first.
func byPosition(t *mobile.Tree, ambiguous []Candidate, positions []int, target *mobile.Node, p Purpose) (Candidate, bool) {
	for _, dynamic := range []bool{false, true} {
		for i, a := range ambiguous {
			if a.Dynamic != dynamic {
				continue
			}
			loc := a.Locator
			loc.Index = positions[i]
			c := Candidate{Locator: loc, Tier: TierIndex, Stability: Weak, Dynamic: a.Dynamic, Matches: a.Matches, Explicit: true,
				Note: fmt.Sprintf("by position %d of %d", positions[i], a.Matches)}
			if confirms(t, c, target, p) {
				return c, true
			}
		}
	}
	return Candidate{}, false
}

func stabilityOf(s string) string {
	if IsDynamic(s) {
		return Weak
	}
	return Medium
}

// rankDynamic moves the locators that are made of changing text behind the others, and keeps the order
// of the tiers inside each group.
func rankDynamic(in []Candidate) []Candidate {
	var stable, dynamic []Candidate
	for _, c := range in {
		if c.Dynamic {
			dynamic = append(dynamic, c)
		} else {
			stable = append(stable, c)
		}
	}
	return append(stable, dynamic...)
}

// confirms says whether the locator finds the target the way a replay would.
func confirms(t *mobile.Tree, c Candidate, target *mobile.Node, p Purpose) bool {
	locs := map[string]domain.Locator{probeName: c.Locator}
	if c.Container != nil {
		locs[probeInner] = c.Container.Locator
	}
	st, got, err := mobile.Resolve(t, locs, probeName)
	if err != nil || !st.Found || got == nil {
		return false
	}
	if c.Container != nil {
		// The container must be one element on its own: a replay finds it, and only it, to search inside.
		cst, cgot, err := mobile.Resolve(t, locs, probeInner)
		if err != nil || !cst.Found || cgot == nil || !inside(got, cgot) {
			return false
		}
	}
	if p == ForTap {
		return tapAccepts(target, got)
	}
	return got == target
}

func inside(n, scope *mobile.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p == scope {
			return true
		}
	}
	return false
}

// tapLeaves collects non-clickable visible descendants inside target (stopping at nested clickables).
func tapLeaves(target *mobile.Node) []*mobile.Node {
	var out []*mobile.Node
	var walk func(*mobile.Node)
	walk = func(n *mobile.Node) {
		for _, k := range n.Children {
			if !k.Visible || Clickable(k) {
				continue
			}
			if k.Text != "" || k.Desc != "" {
				out = append(out, k)
			}
			walk(k)
		}
	}
	walk(target)
	return out
}

// withinCandidates searches for the target inside the nearest ancestor that has a locator of its own
// that finds it alone: an id, a description, or a class with a text that the ancestor shows. The
// container is saved as an element, so it is unique by itself, and the inner locator may then be a text
// that the whole screen shows twice. It climbs past an ancestor in which no inner locator finds the
// target.
func withinCandidates(t *mobile.Tree, target *mobile.Node, p Purpose, ov *Overlay) []Candidate {
	var out []Candidate
	levels := 0
	for anc := target.Parent; anc != nil && levels < maxClimb; anc, levels = anc.Parent, levels+1 {
		cont := containerOf(t, anc, target, ov)
		if cont == nil {
			continue
		}
		inner := []domain.Locator{}
		if target.ID != "" {
			if short := shortID(target.ID); short != target.ID && usable(ov, short) {
				inner = append(inner, domain.Locator{ID: short})
			}
			if usable(ov, target.ID) {
				inner = append(inner, domain.Locator{ID: target.ID})
			}
		}
		if d := collapseSpace(target.Desc); usable(ov, d) {
			inner = append(inner, domain.Locator{Desc: d})
		}
		name := target.AccName()
		if target.Role != "" && usable(ov, name) {
			inner = append(inner, domain.Locator{Role: target.Role, Name: name})
		}
		if usable(ov, target.Text) {
			inner = append(inner, domain.Locator{Text: target.Text})
		}
		if target.Class != "" && usable(ov, target.Class) {
			inner = append(inner, domain.Locator{Class: target.Class})
		}
		found := false
		for _, loc := range inner {
			loc.Within = probeInner
			c := Candidate{Locator: loc, Tier: TierWithin, Stability: Weak, Container: cont, Dynamic: IsDynamic(loc.Text) || IsDynamic(loc.Name) || IsDynamic(loc.Desc)}
			out = append(out, c)
			found = found || confirms(t, c, target, p)
		}
		if found {
			break
		}
	}
	return out
}

// containerOf gives the locator of an ancestor that finds that ancestor alone, or nil. It tries the
// id, then the description, then the class of the ancestor with a text that the ancestor shows beside
// the target.
func containerOf(t *mobile.Tree, anc, target *mobile.Node, ov *Overlay) *Container {
	try := func(loc domain.Locator, hint string) *Container {
		st, got, err := mobile.Resolve(t, map[string]domain.Locator{probeName: loc}, probeName)
		if err != nil || !st.Found || got != anc {
			return nil
		}
		return &Container{Hint: hint, Locator: loc}
	}
	if anc.ID != "" && usable(ov, anc.ID) {
		if c := try(domain.Locator{ID: shortID(anc.ID)}, shortID(anc.ID)); c != nil {
			return c
		}
		if c := try(domain.Locator{ID: anc.ID}, shortID(anc.ID)); c != nil {
			return c
		}
	}
	if d := collapseSpace(anc.Desc); usable(ov, d) && !IsDynamic(d) {
		if c := try(domain.Locator{Desc: d}, d); c != nil {
			return c
		}
	}
	if anc.Class != "" && usable(ov, anc.Class) {
		for _, y := range labelsBeside(anc, target, ov) {
			if c := try(domain.Locator{Class: anc.Class, HasText: y}, y); c != nil {
				return c
			}
		}
	}
	return nil
}

// labelsBeside gives up to maxLabels texts or descriptions that the ancestor shows outside the target
// and outside the nodes that hold the target, in the order of the tree. A label is short, does not look
// like something that changes between runs, and may be part of a saved locator.
func labelsBeside(anc, target *mobile.Node, ov *Overlay) []string {
	var out []string
	add := func(s string) {
		s = collapseSpace(s)
		if len(out) < maxLabels && usable(ov, s) && len([]rune(s)) <= maxLabel && !IsDynamic(s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	var walk func(n *mobile.Node)
	walk = func(n *mobile.Node) {
		for _, k := range n.Children {
			if k == target || !k.Visible || len(out) >= maxLabels {
				continue
			}
			if inside(target, k) {
				// k holds the target: its own labels hold the target's, and only what is inside it counts.
				walk(k)
				continue
			}
			add(k.Desc)
			if len(k.Children) == 0 {
				add(k.Text)
			}
			walk(k)
		}
	}
	walk(anc)
	return out
}

// xpaths gives the shortest xpaths that Semar can run and that hold the attribute of the node: a field
// by its hint, and any other node by its text, description, or resource id. A string that holds both
// kinds of quote cannot be written in an xpath, so it gives none.
func xpaths(n *mobile.Node, editable bool, ov *Overlay) []string {
	class := n.Class
	if class == "" || strings.ContainsAny(class, "[]'\" ") {
		class = "*"
	}
	type attr struct{ name, value string }
	var attrs []attr
	if editable {
		attrs = append(attrs, attr{"hint", n.Hint})
	} else {
		attrs = append(attrs, attr{"text", n.Attrs["text"]}, attr{"content-desc", n.Desc}, attr{"resource-id", n.ID}, attr{"hint", n.Hint})
	}
	var out []string
	for _, a := range attrs {
		if !usable(ov, a.value) || (strings.Contains(a.value, "'") && strings.Contains(a.value, `"`)) {
			continue
		}
		quote := "'"
		if strings.Contains(a.value, "'") {
			quote = `"`
		}
		x := "//" + class + "[@" + a.name + "=" + quote + a.value + quote + "]"
		if _, err := xpathlite.Compile(x); err != nil {
			continue
		}
		out = append(out, x)
	}
	return out
}

// HintOf is what an element is called by: its short id, else its name, else its hint, else its class.
func HintOf(n *mobile.Node) string {
	switch {
	case n.ID != "":
		return shortID(n.ID)
	case n.AccName() != "":
		return n.AccName()
	case n.Hint != "":
		return n.Hint
	}
	_, last, _ := strings.Cut(n.Class, "widget.")
	if last == "" {
		last = n.Class
	}
	return last
}

// ScreenHeading is the name that a screen gets by default: its heading, or the text of the top bar.
// It gives "" when the screen shows neither.
func ScreenHeading(t *mobile.Tree) string {
	if t == nil || t.Root == nil {
		return ""
	}
	var heading, bar string
	top := t.Height / 8
	if top == 0 {
		top = 200
	}
	t.Walk(func(n *mobile.Node) bool {
		if !n.Visible || n.Package == "com.android.systemui" || n.Text == "" || len([]rune(n.Text)) > 40 || IsDynamic(n.Text) {
			return true
		}
		switch {
		case n.Role == "heading" && heading == "":
			heading = n.Text
		case bar == "" && n.Bounds.Bottom <= top && n.Role != "textbox" && !Editable(n) && !Clickable(n) && len(n.Children) == 0:
			bar = n.Text
		}
		return true
	})
	if heading != "" {
		return heading
	}
	return bar
}
