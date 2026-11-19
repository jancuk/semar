package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/report"
)

func webAct(scenario string, blocked ...string) domain.Activity {
	return domain.Activity{Surface: domain.SurfaceWeb, Use: domain.UseCase{Scenario: domain.ScenarioID(scenario), Step: "s1"}, Target: "open /", Result: "done", Blocked: blocked}
}

func TestBlockedHostsGroupByHostAndNameEachScenarioOnce(t *testing.T) {
	acts := []domain.Activity{
		webAct("SHOP-482-02", "cdn.example.com", "127.0.0.1:9000"),
		webAct("SHOP-482-01", "cdn.example.com"),
		webAct("SHOP-482-02", "cdn.example.com"),
		webAct("SHOP-482-03"),
		{Surface: domain.SurfaceAPI, Use: domain.UseCase{Scenario: "SHOP-482-09"}, Blocked: []string{"api.example.com"}},
	}
	got := report.BlockedHosts(acts)
	if len(got) != 2 {
		t.Fatalf("hosts = %+v", got)
	}
	if got[0].Host != "127.0.0.1:9000" || strings.Join(got[0].Met, ",") != "SHOP-482-02" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Host != "cdn.example.com" || strings.Join(got[1].Met, ",") != "SHOP-482-01,SHOP-482-02" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestBlockedLinesSayWhatItMeansAndWhatToDo(t *testing.T) {
	hosts := []report.BlockedHost{
		{Host: "cdn.example.com", Met: []string{"SHOP-482-01", "SHOP-482-02", "SHOP-482-03", "SHOP-482-04", "SHOP-482-05"}},
		{Host: "127.0.0.1:9000", Met: []string{"SHOP-482-02"}},
	}
	var b bytes.Buffer
	report.WriteBlocked(&b, "test", "scenario", hosts)
	out := b.String()
	for _, want := range []string{
		"blocked hosts: the browser did not reach 2 hosts, because environments.test.hosts does not list them\n",
		"  cdn.example.com: SHOP-482-01, SHOP-482-02, SHOP-482-03 and 2 more scenarios\n",
		"  127.0.0.1:9000: SHOP-482-02\n",
		"can pass or fail for the wrong reason",
		"add the host under environments.test.hosts in .semar/semar.yaml, or run semar login, which offers to add them",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the lines lack %q:\n%s", want, out)
		}
	}
	var none bytes.Buffer
	report.WriteBlocked(&none, "test", "scenario", nil)
	if none.Len() != 0 {
		t.Errorf("no host printed %q", none.String())
	}
}

func TestMarkdownListsBlockedHostsWithTheirScenarios(t *testing.T) {
	acts := []domain.Activity{webAct("SHOP-482-01", "cdn.example.com"), webAct("SHOP-482-02", "cdn.example.com")}
	var buf bytes.Buffer
	if err := report.Markdown(&buf, report.Data{Run: domain.Run{ID: "r-1", Environment: "test"}, Activity: acts}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"## Blocked hosts",
		"- cdn.example.com: SHOP-482-01, SHOP-482-02",
		"can pass or fail for the wrong reason",
		"environments.test.hosts",
		"semar login",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	var quiet bytes.Buffer
	if err := report.Markdown(&quiet, report.Data{Run: domain.Run{ID: "r-1", Environment: "test"}, Activity: []domain.Activity{webAct("SHOP-482-01")}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(quiet.String(), "Blocked hosts") {
		t.Errorf("a run with no blocked host has the section:\n%s", quiet.String())
	}
}
