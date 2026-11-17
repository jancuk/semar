package evidence

import (
	"fmt"

	"github.com/jancuk/semar/internal/domain"
)

// LastRunsLooked is how many of the newest runs LastRunOf looks at.
const LastRunsLooked = 30

// ScenarioRun is the result of one scenario in one run.
type ScenarioRun struct {
	Run    domain.RunID
	Result domain.ScenarioResult
}

// LastRunOf finds the newest of the LastRunsLooked newest runs that judged the scenario with the
// expectation hash h. A trial run, a fixture check, and a recording are not runs of a scenario, and
// a run that cannot be read is skipped: one damaged run must not hide the others.
func (s *Store) LastRunOf(id domain.ScenarioID, h domain.Hash) (ScenarioRun, bool, error) {
	if h == "" {
		return ScenarioRun{}, false, nil
	}
	ids, err := s.Runs()
	if err != nil {
		return ScenarioRun{}, false, err
	}
	for _, rid := range ids[:min(len(ids), LastRunsLooked)] {
		run, _, err := s.readRunFile(rid)
		if err != nil || run.Purpose != "" {
			continue
		}
		for _, res := range run.Results {
			if res.Scenario == id && res.ExpectHash == h {
				return ScenarioRun{Run: rid, Result: res}, true, nil
			}
		}
	}
	return ScenarioRun{}, false, nil
}

// Exchanges reads the stored exchange of each step that the run sent for the scenario: the last one
// of the step, when a read was repeated. An exchange that cannot be read is an error, because a
// person who asked for the exchanges should not mistake a missing file for a step that sent nothing.
func (s *Store) Exchanges(run domain.RunID, id domain.ScenarioID) (map[domain.StepID]domain.Action, error) {
	acts, err := s.Activity(run)
	if err != nil {
		return nil, err
	}
	last := map[domain.StepID]domain.Activity{}
	for _, a := range acts {
		u := a.Use
		if u.Scenario != id || u.Step == "" || u.Fixture != nil || a.Evidence == "" {
			continue
		}
		if u.Pass != domain.PassScenario && u.Pass != domain.PassSetup {
			continue
		}
		if prev, ok := last[u.Step]; !ok || a.Seq >= prev.Seq {
			last[u.Step] = a
		}
	}
	out := make(map[domain.StepID]domain.Action, len(last))
	for step, a := range last {
		ex, err := s.ReadAction(run, a.Evidence)
		if err != nil {
			return nil, fmt.Errorf("exchange of step %s: %w", step, err)
		}
		out[step] = ex
	}
	return out, nil
}
