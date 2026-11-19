package cli

import (
	"path/filepath"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/screens"
)

// webEngine makes the browser engine of a run. It starts no Chrome until the first web step needs it,
// so a run with no web step costs nothing. Without a Semar home, a web step ends with that reason.
func (rg *envRig) webEngine(o options, repo string, cfg config.Config, envName string, env config.Environment, headed bool) *web.Engine {
	home, _ := o.home()
	return web.New(web.Options{
		Policy:       rg.policy,
		Recorder:     rg.recorder,
		Files:        rg.run,
		Services:     rg.services,
		Secrets:      rg.secrets,
		Accounts:     webSignIns(cfg),
		Home:         home,
		Environment:  envName,
		Chrome:       env.Web.Chrome,
		Headed:       headed,
		Run:          rg.id,
		Clock:        o.Clock,
		CheckTimeout: env.Web.Timeout,
		Uploads:      filepath.Join(repo, filepath.FromSlash(domain.UploadDir)),
	})
}

// screenSource gives the replay the screens files of the repository.
type screenSource struct{ repo string }

func (s screenSource) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }

// webSignIns is the web sign-in of each account that has one.
func webSignIns(cfg config.Config) map[string]config.WebSignIn {
	out := map[string]config.WebSignIn{}
	for name, p := range cfg.AuthProfiles {
		if p.Web != nil {
			out[name] = *p.Web
		}
	}
	return out
}
