package cli

import (
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/screens"
)

// webEngine makes the browser engine of a run. It starts no Chrome until the first web step needs it,
// so a run with no web step costs nothing. Without a Semar home, a web step ends with that reason.
func (rg *envRig) webEngine(o options, envName string, env config.Environment, headed bool) *web.Engine {
	home, _ := o.home()
	return web.New(web.Options{
		Policy:      rg.policy,
		Recorder:    rg.recorder,
		Files:       rg.run,
		Services:    rg.services,
		Secrets:     rg.secrets,
		Home:        home,
		Environment: envName,
		Chrome:      env.Web.Chrome,
		Headed:      headed,
		Run:         rg.id,
		Clock:       o.Clock,
	})
}

// screenSource gives the replay the screens files of the repository and their approvals.
type screenSource struct {
	repo      string
	approvals *confirm.Local
	// trusted is an app whose screens count as approved in this run, with no approval stored. Only
	// the replay that semar record makes of its own recording sets it.
	trusted string
}

func (s screenSource) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }

func (s screenSource) Approved(app string, h domain.Hash) (bool, error) {
	if s.trusted != "" && app == s.trusted {
		return true, nil
	}
	return s.approvals.ScreensApproved(app, h)
}
