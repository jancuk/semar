package verdict

import (
	"fmt"

	"github.com/jancuk/semar/internal/domain"
)

// docFailures are the reasons that the check against the API document gives for a step that is
// not a setup step. A check in warn mode, and a status that the document does not declare, give none:
// they are printed and never change an outcome.
func docFailures(step domain.Step, a *domain.Action) []string {
	if a == nil || a.Doc == nil || step.Setup {
		return nil
	}
	d := a.Doc
	var out []string
	if d.SchemaFail && len(d.Misfits) > 0 {
		out = append(out, fmt.Sprintf("step %s: the response does not fit the API document: %s; to report it as a warning, set %s to warn",
			step.ID, d.SchemaText(), setting(d.Env, "checks.schema")))
	}
	if d.SlowFail && d.Slow() {
		out = append(out, fmt.Sprintf("step %s: the response took %d ms, over the limit of %d ms; to report it as a warning, set %s to warn",
			step.ID, d.TookMS, d.LimitMS, setting(d.Env, "checks.responseTime.on")))
	}
	return out
}

func setting(env, key string) string {
	if env == "" {
		return "environments.<name>." + key
	}
	return "environments." + env + "." + key
}
