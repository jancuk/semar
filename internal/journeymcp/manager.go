package journeymcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
)

var legIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Manager keeps the one draft journey of a tool server and runs the journey tools. It is safe for
// concurrent use; every tool takes the lock.
type Manager struct {
	o     Options
	scrub *mcpkit.Scrubber
	clock func() time.Time

	mu sync.Mutex
	d  *draft
}

// New makes a Manager.
func New(o Options) *Manager {
	m := &Manager{o: o, scrub: o.Scrub, clock: o.Clock}
	if m.scrub == nil {
		m.scrub = mcpkit.NewScrubber(nil)
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	return m
}

// draft is the journey that is being made.
type draft struct {
	id          domain.JourneyID
	title       string
	owner       string
	criticality string
	authProfile string
	replace     bool
	// author is who writes the journey: the agent, by the name that its client gave when it started it.
	author domain.ProviderRef

	// legs are the closed legs, in order. cur is the open leg, or nil.
	legs []*closedLeg
	cur  *openLeg
	// n counts the legs that were opened for each surface, for the names api_1, web_1, android_1.
	n map[domain.Surface]int
}

// closedLeg is a leg with everything that its closing made.
type closedLeg struct {
	leg   domain.Leg
	flows []domain.Flow
	files []ScreensFile
	// from is the leg as it was open, so that a failed save can open it again. It is nil for an android
	// leg, whose recording is over.
	from *openLeg
	note []string
}

// openLeg is a leg that things are being recorded into.
type openLeg struct {
	id      domain.LegID
	surface domain.Surface
	service string
	app     string
	as      string
	needs   []domain.LegID
	wait    *domain.LegWait
	auto    bool

	apiMark int

	webMark int
	webURL  string
	webHad  bool

	flowID      string
	flowStarted bool

	// buildNote is what the backend said about the build of the service when the leg was opened, or "".
	buildNote string
}

func (m *Manager) fail(format string, args ...any) error {
	return errors.New(m.scrub.Text(fmt.Sprintf(format, args...)))
}

func (m *Manager) errResult(err error) mcp.Result { return mcp.ErrorResult(m.scrub.Text(err.Error())) }

// needDraft is the draft, or the error that says to start one.
func (m *Manager) needDraft() (*draft, error) {
	if m.d == nil {
		return nil, errors.New("there is no draft journey: start one with semar_journey_start")
	}
	return m.d, nil
}

// legIDs are the IDs of the closed legs and of the open one.
func (d *draft) taken(id domain.LegID) bool {
	if d.cur != nil && d.cur.id == id {
		return true
	}
	return slices.ContainsFunc(d.legs, func(l *closedLeg) bool { return l.leg.ID == id })
}

// previous is the leg that the next leg needs by default: the open leg, or else the last closed one.
func (d *draft) previous() []domain.LegID {
	switch {
	case d.cur != nil:
		return []domain.LegID{d.cur.id}
	case len(d.legs) > 0:
		return []domain.LegID{d.legs[len(d.legs)-1].leg.ID}
	}
	return nil
}

// autoID is the next free name of a leg of a surface: api_1, web_2, android_1.
func (d *draft) autoID(s domain.Surface) domain.LegID {
	for {
		d.n[s]++
		id := domain.LegID(fmt.Sprintf("%s_%d", s, d.n[s]))
		if !d.taken(id) {
			return id
		}
	}
}

// reach is every leg that the legs in needs reach, directly or through others, among the closed legs.
func (d *draft) reach(needs []domain.LegID) []*closedLeg {
	seen := map[domain.LegID]bool{}
	var walk func(ids []domain.LegID)
	walk = func(ids []domain.LegID) {
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			for _, l := range d.legs {
				if l.leg.ID == id {
					walk(l.leg.Needs)
				}
			}
		}
	}
	walk(needs)
	var out []*closedLeg
	for _, l := range d.legs {
		if seen[l.leg.ID] {
			out = append(out, l)
		}
	}
	return out
}

// openLegLocked opens a leg: the marks are taken now, so that the leg holds what happens from here on.
// The caller has closed the leg that was open.
func (m *Manager) openLegLocked(ctx context.Context, d *draft, l *openLeg) error {
	switch l.surface {
	case domain.SurfaceAPI:
		if m.o.API == nil {
			return errors.New("this server has no api tools, so it cannot make an api leg")
		}
		l.apiMark = m.o.API.Seq()
	case domain.SurfaceWeb:
		if m.o.Web == nil {
			return errors.New("this server has no web tools, so it cannot make a web leg")
		}
		if mark, url, err := m.o.Web.Mark(ctx); err == nil {
			l.webMark, l.webURL, l.webHad = mark, url, true
		}
		if l.app == "" {
			l.app = l.service
		}
	case domain.SurfaceAndroid:
		if m.o.Mobile == nil {
			return errors.New("this server has no mobile tools, so it cannot make an android leg")
		}
		l.flowID = string(d.id) + "-" + string(l.id)
	default:
		return fmt.Errorf("surface %q is not known: it is api, web, or android", l.surface)
	}
	if bc, ok := m.o.Backend.(BuildChecker); ok {
		l.buildNote = bc.BuildProblem(ctx, l.service)
	}
	d.cur = l
	return nil
}

// closeCurLocked closes the open leg, if there is one. A leg with no step is dropped, and the second
// result says so. On an error the leg stays open.
func (m *Manager) closeCurLocked(ctx context.Context, d *draft) (dropped string, err error) {
	l := d.cur
	if l == nil {
		return "", nil
	}
	built, err := m.buildLeg(ctx, d, l)
	switch {
	case errors.Is(err, errEmptyLeg):
		d.cur = nil
		return fmt.Sprintf("leg %s had no step and was dropped", l.id), nil
	case err != nil:
		return "", err
	}
	d.legs = append(d.legs, built)
	d.cur = nil
	return "", nil
}

// reopenLast puts the last closed leg back as the open one, when its surface allows it. It is how a
// save that did not hold lets the agent fix the leg.
func (d *draft) reopenLast() bool {
	n := len(d.legs)
	if n == 0 || d.cur != nil || d.legs[n-1].from == nil {
		return false
	}
	d.cur = d.legs[n-1].from
	d.legs = d.legs[:n-1]
	return true
}

// Tools gives the journey tools in a fixed order.
func (m *Manager) Tools() []mcp.Tool {
	defs := m.defs()
	tools := make([]mcp.Tool, 0, len(defs))
	for _, d := range defs {
		ann := mcp.Annotations{Title: d.title, ReadOnly: mcp.BoolPtr(!d.write), OpenWorld: mcp.BoolPtr(d.openWorld)}
		if d.write {
			ann.Destructive = mcp.BoolPtr(d.destructive)
		}
		tools = append(tools, mcp.Tool{
			Name: d.name, Title: d.title, Description: d.desc, Input: d.schema, Annotations: ann,
			Surface: string(mcpkit.SurfaceJourney), Write: d.write,
			Handler: func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
				return m.invoke(ctx, d, call), nil
			},
		})
	}
	return tools
}

type toolDef struct {
	name, title, desc string
	schema            mcp.Schema
	write             bool
	destructive       bool
	openWorld         bool
	run               func(ctx context.Context, call mcp.Call) (mcp.Result, error)
}

// invoke runs one tool: the guard, the lock, the tool, and the scrub.
func (m *Manager) invoke(ctx context.Context, d *toolDef, call mcp.Call) mcp.Result {
	if err := m.o.Guard.Allow(mcpkit.SurfaceJourney, d.write); err != nil {
		return m.errResult(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	res, err := d.run(ctx, call)
	if err != nil {
		return m.errResult(err)
	}
	for i, c := range res.Content {
		if c.Type == "text" {
			res.Content[i].Text = m.scrub.Text(c.Text)
		}
	}
	return res
}

// Active reports whether a draft is open.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d != nil
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }
