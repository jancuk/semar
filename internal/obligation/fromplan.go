package obligation

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/stage/plan"
)

// The prefixes of the names that a plan gives. A kind is the part of a name before its number.
const (
	kindScenario    = "scenario-"
	kindNotDrafted  = "not-drafted-"
	kindUncovered   = "uncovered-"
	kindDifference  = "difference-"
	kindManualCheck = "manual-"
	kindCriterion   = "criterion-"
)

// citePattern is how the planner starts a cited place: <file>:<line>.
var citePattern = regexp.MustCompile(`^([^\s:]+):([0-9]+)`)

var gapKinds = []string{kindNotDrafted, kindUncovered, kindDifference, kindManualCheck, kindCriterion}

// FromPlan returns the obligations file of each ticket that the plan names, with the entries of
// the existing files kept as they are and the new entries added after them (D3). It never
// removes or changes an entry, because a person may have edited the file. It does not change
// existing. Every text from the draft is cut and made one line here, and the YAML encoder
// writes it as one string, so a text cannot add a key.
func FromPlan(cs domain.ChangeSet, d plan.Draft, scenarios []domain.Scenario, existing map[string]File) (map[string]File, error) {
	return FromPlanWith(cs, d, scenarios, nil, existing)
}

// FromPlanWith is FromPlan with the acceptance criteria that nothing in the plan cites. Each is
// written as an obligation with a gap and no checklist, so the ledger still shows it (D9).
func FromPlanWith(cs domain.ChangeSet, d plan.Draft, scenarios []domain.Scenario, unaccounted []plan.Criterion, existing map[string]File) (map[string]File, error) {
	b := &builder{cs: cs, files: map[string]*building{}, existing: existing}

	for _, sc := range scenarios {
		t, err := b.ticket(sc.Ticket)
		if err != nil {
			return nil, fmt.Errorf("scenario %s: %w", sc.ID, err)
		}
		title := oneLine(sc.Title, maxTitle)
		if title == "" {
			title = string(sc.ID)
		}
		o := Obligation{ID: t.key + "/" + kindScenario + string(sc.ID), Title: title, Source: SourceDiff, Covers: []domain.ScenarioID{sc.ID}}
		if err := b.addOnce(t, o); err != nil {
			return nil, err
		}
	}
	for _, nd := range d.NotDrafted {
		key := nd.Ticket
		if key == "" && len(cs.Tickets) > 0 {
			key = cs.Tickets[0].Key
		}
		t, err := b.ticket(key)
		if err != nil {
			return nil, fmt.Errorf("a case that was not drafted: %w", err)
		}
		if err := b.addGap(t, kindNotDrafted, Obligation{
			Title: oneLine(nd.What, maxTitle), Source: SourceDiff, Gap: oneLine("not drafted: needs "+nd.Needs, maxGap),
		}); err != nil {
			return nil, err
		}
	}
	for _, u := range d.Uncovered {
		key, ok := b.ticketOfFile(u.File)
		if !ok {
			return nil, fmt.Errorf("the uncovered file %s has no ticket: the change set names none", oneLine(u.File, 100))
		}
		t, err := b.ticket(key)
		if err != nil {
			return nil, fmt.Errorf("the uncovered file %s: %w", oneLine(u.File, 100), err)
		}
		// No scenario reaching a changed file is a decision for a person, not a missing piece:
		// the change may be covered by a manual check, or change no behaviour. Blocked would
		// stay blocked forever, so the file goes on the checklist with the planner's reason.
		file := oneLine(u.File, 200)
		if err := b.addGap(t, kindUncovered, Obligation{
			Title:  oneLine("Changes in "+file, maxTitle),
			Source: SourceDiff,
			Manual: &Manual{
				Kind: "judgement",
				Text: oneLine("No scenario covers "+file+". The planner says: "+u.Reason, maxManual),
				Steps: []string{
					oneLine("Read the change in "+file+".", maxManual),
					"Decide whether a scenario or a manual check of this ticket covers it, or whether it changes no behaviour.",
				},
				Expected:  "The change is covered by a scenario or a manual check, or it changes no behaviour",
				WrittenBy: "code",
			},
		}); err != nil {
			return nil, err
		}
	}
	for _, df := range d.Differences {
		t, err := b.ticket(df.Ticket)
		if err != nil {
			return nil, fmt.Errorf("a difference: %w", err)
		}
		docCite, docText := splitCite(df.Document)
		codeCite, codeText := splitCite(df.Code)
		if err := b.addGap(t, kindDifference, Obligation{
			Title:  oneLine("The document and the code differ: "+df.What, maxTitle),
			Source: SourceKnowledge,
			Cite:   oneLine(docCite, maxCite),
			Manual: &Manual{
				Kind:      "judgement",
				Text:      "A person decides whether the document or the code is right",
				Steps:     []string{readStep(docCite, docText), readStep(codeCite, codeText), "Decide which one is right, and change the other, or the scenario."},
				Expected:  "The document and the code agree",
				WrittenBy: "code",
			},
		}); err != nil {
			return nil, err
		}
	}
	for _, mc := range d.ManualChecks {
		t, err := b.ticket(mc.Ticket)
		if err != nil {
			return nil, fmt.Errorf("a manual check: %w", err)
		}
		steps := make([]string, 0, len(mc.Steps))
		for _, st := range mc.Steps {
			if line := oneLine(st, maxManual); line != "" {
				steps = append(steps, line)
			}
		}
		if err := b.addGap(t, kindManualCheck, Obligation{
			Title:  oneLine(mc.Title, maxTitle),
			Source: SourceCriterion,
			Cite:   oneLine(mc.Cite, maxCite),
			Manual: &Manual{
				Kind:      "judgement",
				Text:      "Only a person can check this criterion, on a screen, on a device, or by a gesture",
				Steps:     steps,
				Expected:  oneLine(mc.Expected, maxManual),
				WrittenBy: "ai",
			},
		}); err != nil {
			return nil, err
		}
	}
	for _, c := range unaccounted {
		t, err := b.ticket(c.Ticket)
		if err != nil {
			return nil, fmt.Errorf("a criterion that the planner did not account for: %w", err)
		}
		if err := b.addGap(t, kindCriterion, Obligation{
			Title:  oneLine("Criterion "+strconv.Itoa(c.Number)+": "+c.Text, maxTitle),
			Source: SourceCriterion,
			Cite:   oneLine(c.Cite(), maxCite),
			Gap:    "criterion not accounted for by the planner",
		}); err != nil {
			return nil, err
		}
	}

	out := map[string]File{}
	for _, key := range slices.Sorted(maps.Keys(b.files)) {
		t := b.files[key]
		if len(t.entries) > MaxPerFile {
			return nil, fmt.Errorf("the obligations file of %s would hold %d entries, and the limit is %d: remove the ones that no longer apply, in a reviewed commit", key, len(t.entries), MaxPerFile)
		}
		out[key] = File{APIVersion: apiVersion, Kind: kind, Ticket: key, Obligations: t.entries}
	}
	return out, nil
}

// building is the file of one ticket while entries are added to it.
type building struct {
	key     string
	entries []Obligation
	ids     map[string]bool
	titles  map[string]map[string]bool // kind to the folded titles of that kind
	highest map[string]int             // kind to the highest number
}

type builder struct {
	cs       domain.ChangeSet
	files    map[string]*building
	existing map[string]File
}

// ticket returns the file being built for a ticket, starting from the existing one.
func (b *builder) ticket(key string) (*building, error) {
	if err := idcheck.Check("ticket", key); err != nil {
		return nil, err
	}
	if t, ok := b.files[key]; ok {
		return t, nil
	}
	t := &building{key: key, ids: map[string]bool{}, titles: map[string]map[string]bool{}, highest: map[string]int{}}
	if f, ok := b.existing[key]; ok {
		if f.Ticket != "" && f.Ticket != key {
			return nil, fmt.Errorf("the existing obligations file of %s is for ticket %s", key, f.Ticket)
		}
		t.entries = slices.Clone(f.Obligations)
		for _, o := range t.entries {
			t.note(o)
		}
	}
	b.files[key] = t
	return t, nil
}

func (t *building) note(o Obligation) {
	t.ids[o.ID] = true
	_, name, _ := strings.Cut(o.ID, "/")
	for _, kind := range gapKinds {
		rest, ok := strings.CutPrefix(name, kind)
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(rest); err == nil && n > t.highest[kind] {
			t.highest[kind] = n
		}
		if t.titles[kind] == nil {
			t.titles[kind] = map[string]bool{}
		}
		t.titles[kind][fold(o.Title)] = true
	}
}

func (b *builder) add(t *building, o Obligation) error {
	if len(o.ID) > maxIDBytes {
		return fmt.Errorf("the id %s is longer than %d bytes: use a shorter scenario ID", o.ID[:40]+"...", maxIDBytes)
	}
	t.entries = append(t.entries, o)
	t.note(o)
	return nil
}

// addOnce adds an entry unless the file holds its ID already, edited or not.
func (b *builder) addOnce(t *building, o Obligation) error {
	if t.ids[o.ID] {
		return nil
	}
	return b.add(t, o)
}

// addGap numbers an entry of a kind after the highest number so far. An entry whose folded
// title equals one of the same kind is not added again.
func (b *builder) addGap(t *building, kind string, o Obligation) error {
	if o.Title == "" {
		o.Title = strings.TrimSuffix(kind, "-") + " entry"
	}
	if t.titles[kind][fold(o.Title)] {
		return nil
	}
	o.ID = t.key + "/" + kind + strconv.Itoa(t.highest[kind]+1)
	return b.add(t, o)
}

// ticketOfFile is the first ticket, in key order, whose files hold the file, else the first
// ticket of the change set.
func (b *builder) ticketOfFile(file string) (string, bool) {
	tickets := slices.SortedFunc(slices.Values(b.cs.Tickets), func(a, c domain.TicketChange) int { return strings.Compare(a.Key, c.Key) })
	for _, t := range tickets {
		if slices.Contains(t.Files, file) {
			return t.Key, true
		}
	}
	if len(tickets) > 0 {
		return tickets[0].Key, true
	}
	return "", false
}

// fold makes two titles equal when they differ only in spaces and case.
func fold(s string) string { return strings.ToLower(oneLine(s, 1<<20)) }

// oneLine makes untrusted text one line of at most limit characters: any run of white space
// becomes one space, and a control or format character is dropped.
func oneLine(s string, limit int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if !unicode.IsSpace(r) && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > limit {
		s = strings.TrimSpace(string([]rune(s)[:limit]))
	}
	return s
}

// splitCite splits a text that starts with <file>:<line> into that place and the rest.
func splitCite(text string) (cite, rest string) {
	text = strings.TrimSpace(text)
	m := citePattern.FindStringSubmatch(text)
	if m == nil {
		return text, ""
	}
	cite = m[1] + ":" + m[2]
	rest = strings.TrimLeft(strings.TrimPrefix(text, cite), ": \t\r\n")
	return cite, rest
}

func readStep(cite, rest string) string {
	if rest == "" {
		return oneLine("Read "+cite+".", maxManual)
	}
	return oneLine("Read "+cite+": "+rest, maxManual)
}
