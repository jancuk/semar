package report_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/report"
)

// missAct is an Android activity with one element that was not found.
func missAct(scenario, step, element string, repair, nearby int) domain.Activity {
	m := domain.ElementMiss{App: "shop-app", Screen: "Cart", Element: element, Looked: domain.Locator{Role: "button", Name: "Hapus"}}
	for i := 0; i < repair; i++ {
		m.Repair = append(m.Repair, domain.Candidate{Element: element, Locator: domain.Locator{Role: "button", Name: fmt.Sprintf("Hapus %d", i)}})
	}
	for i := 0; i < nearby; i++ {
		m.Nearby = append(m.Nearby, domain.Candidate{Element: element, Why: fmt.Sprintf(`button "Close %d"`, i)})
	}
	return domain.Activity{Surface: domain.SurfaceAndroid, Use: domain.UseCase{Scenario: domain.ScenarioID(scenario), Step: domain.StepID(step)},
		Target: "tap Cart", Result: "error: an element was not found", Misses: []domain.ElementMiss{m}}
}

func TestMissesListEachElementOnceAndKeepTheLastOfAStep(t *testing.T) {
	acts := []domain.Activity{
		missAct("AND-1", "s1", "delete", 0, 1),
		missAct("AND-1", "s1", "delete", 2, 3),
		missAct("AND-2", "s1", "delete", 0, 0),
		{Surface: domain.SurfaceWeb, Misses: []domain.ElementMiss{{Element: "web"}}},
	}
	got := report.Misses(acts)
	if len(got) != 2 {
		t.Fatalf("misses = %+v", got)
	}
	if got[0].Scenario != "AND-1" || got[0].Candidates != 2 || len(got[0].Closest) != 3 || got[0].Looked != `{ role: button, name: Hapus }` {
		t.Errorf("first = %+v", got[0])
	}
}

func TestWriteMissesSaysWhatWasNotFoundAndHowToRepairIt(t *testing.T) {
	var b bytes.Buffer
	report.WriteMisses(&b, "r-1", report.Misses([]domain.Activity{missAct("AND-1", "s1", "delete", 2, 2), missAct("AND-2", "s2", "back", 0, 1)}))
	out := b.String()
	for _, want := range []string{
		"elements not found: 2\n",
		"  AND-1 step s1: Cart.delete was not found (looked for { role: button, name: Hapus })\n",
		"    closest on the screen: button \"Close 0\"; button \"Close 1\"\n",
		"    to use the first candidate: semar screens repair r-1 --apply Cart.delete=1\n",
		"    to see the screen: semar screens repair r-1\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the lines lack %q:\n%s", want, out)
		}
	}
	var none bytes.Buffer
	report.WriteMisses(&none, "r-1", nil)
	if none.Len() != 0 {
		t.Errorf("no miss printed %q", none.String())
	}
}

func TestWriteMissesStopsAtFiveAndCountsTheRest(t *testing.T) {
	var acts []domain.Activity
	for i := 0; i < 8; i++ {
		acts = append(acts, missAct("AND-1", fmt.Sprintf("s%d", i), "delete", 0, 1))
	}
	var b bytes.Buffer
	report.WriteMisses(&b, "r-1", report.Misses(acts))
	out := b.String()
	if !strings.Contains(out, "elements not found: 8\n") || !strings.Contains(out, "and 3 more: semar screens repair r-1 lists them all") {
		t.Errorf("lines:\n%s", out)
	}
	if n := strings.Count(out, "was not found"); n != 5 {
		t.Errorf("%d misses shown, want 5:\n%s", n, out)
	}
	var md bytes.Buffer
	if err := report.Markdown(&md, report.Data{Run: domain.Run{ID: "r-1", Environment: "test"}, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(md.String(), "(looked for"); n != 5 || !strings.Contains(md.String(), "and 3 more") {
		t.Errorf("report shows %d misses:\n%s", n, md.String())
	}
}

func TestReportsHaveAnElementsNotFoundSection(t *testing.T) {
	acts := []domain.Activity{missAct("AND-1", "s1", "delete", 1, 2)}
	var md bytes.Buffer
	if err := report.Markdown(&md, report.Data{Run: domain.Run{ID: "r-1", Environment: "test"}, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Elements not found",
		"- AND-1, step s1: Cart.delete (looked for { role: button, name: Hapus }). Closest: button \"Close 0\"; button \"Close 1\". Run `semar screens repair r-1 --apply Cart.delete=1`",
	} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("report.md lacks %q:\n%s", want, md.String())
		}
	}
	var h bytes.Buffer
	if err := report.HTML(&h, report.Data{Run: domain.Run{ID: "r-1", Environment: "test"}, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h2>Elements not found</h2>", "Cart.delete", "semar screens repair r-1 --apply Cart.delete=1"} {
		if !strings.Contains(h.String(), want) {
			t.Errorf("report.html lacks %q", want)
		}
	}
	var quiet bytes.Buffer
	if err := report.Markdown(&quiet, report.Data{Run: domain.Run{ID: "r-1", Environment: "test"}, Activity: []domain.Activity{webAct("AND-1")}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet.String(), "Elements not found") {
		t.Error("a run with no miss has the section")
	}
}

// The activity of a stored device action carries its misses, and a restart shows in the result.
func TestActivityOfADeviceActionCarriesItsMissesAndARestart(t *testing.T) {
	a := domain.Action{Surface: domain.SurfaceAndroid, Err: "an element was not found", Mobile: &domain.MobileRecord{
		Op: "tap", App: "shop-app", Screen: "Cart", Recovered: true,
		Elements: map[string]domain.ElementState{
			"delete": {Locator: &domain.Locator{Role: "button", Name: "Hapus"}},
			"title":  {Locator: &domain.Locator{Text: "Cart"}, Presence: true},
			"found":  {Found: true, Count: 1, Locator: &domain.Locator{ID: "x"}},
		},
		Repair: []domain.Candidate{{Element: "delete", Locator: domain.Locator{Role: "button", Name: "Hapus Semua"}}},
		Nearby: []domain.Candidate{{Element: "delete", Why: `button "Hapus Semua"`}, {Element: "other", Why: "x"}},
	}}
	act := activity.FromAction(a, effects.Write, "actions/a.json")
	if len(act.Misses) != 1 || act.Misses[0].Element != "delete" || len(act.Misses[0].Repair) != 1 || len(act.Misses[0].Nearby) != 1 || act.Misses[0].Screen != "Cart" {
		t.Fatalf("misses = %+v", act.Misses)
	}
	if !strings.Contains(act.Result, "the UiAutomator2 server was restarted") {
		t.Errorf("result = %q", act.Result)
	}
}

// A crash that the engine stored reaches the activity: the failure line says that the app crashed, and the
// two files of evidence are named.
func TestActivityOfADeviceActionCarriesItsCrash(t *testing.T) {
	a := domain.Action{Surface: domain.SurfaceAndroid, Err: "an element was not found", Mobile: &domain.MobileRecord{
		Op: "tap", Screen: "Cart", LogcatFile: "logcat-pay.txt",
		Crash: &domain.MobileCrash{Summary: "java.lang.IllegalStateException: Cart is empty", File: "crash-pay.txt"},
	}}
	act := activity.FromAction(a, effects.Write, "actions/a.json")
	if want := "error: an element was not found; the app crashed: java.lang.IllegalStateException: Cart is empty"; act.Result != want {
		t.Errorf("result = %q, want %q", act.Result, want)
	}
	if act.CrashFile != "crash-pay.txt" || act.LogcatFile != "logcat-pay.txt" {
		t.Errorf("files = %q, %q", act.CrashFile, act.LogcatFile)
	}
	plain := activity.FromAction(domain.Action{Surface: domain.SurfaceAndroid, Mobile: &domain.MobileRecord{Op: "tap"}}, effects.Read, "a")
	if plain.CrashFile != "" || plain.LogcatFile != "" || strings.Contains(plain.Result, "crashed") {
		t.Errorf("plain = %+v", plain)
	}
}
