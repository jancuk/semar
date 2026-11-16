package obligation_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/obligation"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func checklistEntries() []obligation.Entry {
	mk := func(id, title string, src obligation.Source, cite, by string, steps ...string) obligation.Entry {
		o := ob(id, func(o *obligation.Obligation) {
			o.Title, o.Source, o.Cite = title, src, cite
			o.Manual = &obligation.Manual{Kind: "judgement", Text: "A person decides whether the document or the code is right", Steps: steps, Expected: "The document and the code agree", WrittenBy: by}
		})
		return obligation.Entry{Obligation: o, State: obligation.NeedsAPerson}
	}
	byCode := mk("SHOP-482/difference-1", "The document and the code differ: the status of a refund above the total", obligation.SourceKnowledge, "docs/refunds.md:14", "code",
		"Read docs/refunds.md:14: the refusal is 409.", "Read internal/refund/handler.go:41: the refusal is 422.", "Decide which one is right, and change the other, or the scenario.")
	byPerson := mk("SHOP-482/mailbox", "The customer gets an email about the rejection", obligation.SourceTestcase, "TC-1042: the customer is told by email", "person",
		"Sign in to the test mailbox of semar-qa-1.", "Find the email sent after the run.")
	byPerson.Manual = &obligation.ManualResult{Result: "failed", User: "tester", At: now}
	byAI := mk("refunds/ai-case", "Case proposed by a model", obligation.SourceDiff, "", "ai", "Look at the screen.")
	notManual := obligation.Entry{Obligation: ob("SHOP-482/other", nil), State: obligation.Blocked}
	return []obligation.Entry{byCode, notManual, byPerson, byAI}
}

func TestChecklistGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := obligation.Checklist(&buf, checklistEntries(), ""); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "checklist.golden")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Errorf("checklist differs from %s:\n%s", golden, buf.String())
	}
	for _, label := range []string{"Steps (written by code):", "Steps (written by a person):", "Steps (written by AI):"} {
		if !strings.Contains(buf.String(), label) {
			t.Errorf("no label %q", label)
		}
	}
}

func TestChecklistOneGroup(t *testing.T) {
	var buf bytes.Buffer
	if err := obligation.Checklist(&buf, checklistEntries(), "refunds"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "Manual checks for refunds (1)\n1. refunds/ai-case") || strings.Contains(out, "SHOP-482") {
		t.Errorf("got:\n%s", out)
	}
	buf.Reset()
	if err := obligation.Checklist(&buf, checklistEntries(), "nothing"); err != nil || buf.Len() != 0 {
		t.Errorf("an empty group wrote %q, %v", buf.String(), err)
	}
}

func TestChecklistPrintsTextAsData(t *testing.T) {
	es := checklistEntries()[:1]
	es[0].Obligation.Title = "title \x1b[2J"
	es[0].Obligation.Manual.Steps = []string{"step \x1b]0;pwn\x07"}
	var buf bytes.Buffer
	if err := obligation.Checklist(&buf, es, ""); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), "\x1b\x07") {
		t.Errorf("a control character reached the terminal: %q", buf.String())
	}
}
