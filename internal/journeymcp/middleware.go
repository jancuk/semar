package journeymcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mcp"
)

// mobileActing are the mobile tools that act on the app: the first of them in an android leg starts the
// recording of its flow.
var mobileActing = map[string]bool{
	"mobile_click_on_screen_at_coordinates": true, "mobile_double_tap_on_screen": true,
	"mobile_long_press_on_screen_at_coordinates": true, "mobile_drag_on_screen": true,
	"mobile_swipe_on_screen": true, "mobile_type_keys": true, "mobile_press_button": true,
	"mobile_open_url": true, "mobile_launch_app": true, "mobile_batch_commands": true, runFlowTool: true,
}

// screenBound are the acting tools that aim at what the screen showed: a coordinate or a ref of the screen
// that the agent saw. A link and a saved flow do not depend on the screen, so they are not among them.
var screenBound = map[string]bool{
	"mobile_click_on_screen_at_coordinates": true, "mobile_double_tap_on_screen": true,
	"mobile_long_press_on_screen_at_coordinates": true, "mobile_drag_on_screen": true,
	"mobile_swipe_on_screen": true, "mobile_type_keys": true, "mobile_press_button": true,
	"mobile_batch_commands": true,
}

const (
	runFlowTool = "semar_run_flow"
	launchTool  = "mobile_launch_app"
)

// legKind says which kind of leg a tool works on, or "" when it works on none.
func legKind(t mcp.Tool) domain.Surface {
	switch t.Surface {
	case "api":
		if t.Name == "api_request" {
			return domain.SurfaceAPI
		}
	case "web":
		// web_open only reads, but it is how a web leg begins.
		if t.Write && t.Name != "web_save" || t.Name == "web_open" {
			return domain.SurfaceWeb
		}
	case "mobile":
		if mobileActing[t.Name] {
			return domain.SurfaceAndroid
		}
	}
	return ""
}

// AutoLegMiddleware opens the legs of the draft journey. While a draft is open, a tool that acts on a
// surface needs a leg of that surface and service: when there is none, the open leg is closed and the next
// one, api_1, web_1, android_1, and so on, is opened with needs on the one before. The tool then runs. When
// no draft is open the middleware does nothing.
func (m *Manager) AutoLegMiddleware() mcp.Middleware {
	return func(t mcp.Tool, next mcp.Handler) mcp.Handler {
		if ownedByLeg[t.Name] {
			return func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
				if err := m.refuseRecordTool(t.Name); err != nil {
					return m.errResult(err), nil
				}
				return next(ctx, call)
			}
		}
		if t.Surface == "web" && endsWebWindow[t.Name] {
			return func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
				if err := m.refuseWebEnd(t.Name); err != nil {
					return m.errResult(err), nil
				}
				return next(ctx, call)
			}
		}
		kind := legKind(t)
		if kind == "" {
			return next
		}
		return func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
			note, started, err := m.beforeAction(ctx, kind, t.Name, call.Args)
			if err != nil {
				return m.errResult(err), nil
			}
			if started && t.Name == launchTool {
				// The recording has started the app from a fresh start, which is what a launch would do.
				return mcp.TextResult(strings.TrimSpace(note + "\njourney: the recording of the leg started the app from a fresh start, so the launch was not sent again. List the screen with mobile_list_elements_on_screen and go on.")), nil
			}
			if started && screenBound[t.Name] {
				// The recording restarted the app, so the screen that the action aimed at is gone. Sending it
				// would hit whatever sits at that place on the welcome screen.
				text := note + "\njourney: the recording of the leg restarted the app from a fresh start, so " + t.Name + " was not sent: the screen it aimed at is gone. The screen now is below. Act again with its refs."
				if screen, err := m.o.Mobile.LegScreen(ctx); err != nil {
					text = note + "\njourney: the recording of the leg restarted the app from a fresh start, so " + t.Name + " was not sent: the screen it aimed at is gone. List the screen with mobile_list_elements_on_screen and act again."
				} else {
					text += "\n" + screen
				}
				return mcp.TextResult(m.scrub.Text(strings.TrimSpace(text))), nil
			}
			res, err := next(ctx, call)
			if note != "" {
				res.Content = append(res.Content, mcp.Content{Type: "text", Text: m.scrub.Text(note)})
			}
			return res, err
		}
	}
}

// beforeAction makes sure that a leg is open for the action. It returns a note for the model when it
// opened or closed a leg, and whether this action started the recording of an android leg.
func (m *Manager) beforeAction(ctx context.Context, kind domain.Surface, tool string, raw json.RawMessage) (note string, started bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.d
	if d == nil {
		return "", false, nil
	}
	var a struct {
		Service string `json:"service"`
	}
	_ = json.Unmarshal(raw, &a)

	var notes []string
	service := ""
	switch kind {
	case domain.SurfaceAPI:
		if m.o.API == nil || a.Service == "" {
			return "", false, nil
		}
		service = a.Service
	case domain.SurfaceWeb:
		if m.o.Web == nil {
			return "", false, nil
		}
		if service = m.o.Web.Service(); service == "" {
			service = a.Service
		}
		if service == "" && tool == "web_open" {
			service = m.o.Web.DefaultService()
		}
		if service == "" {
			return "", false, nil
		}
	case domain.SurfaceAndroid:
		if m.o.Mobile == nil {
			return "", false, nil
		}
		if d.cur != nil && d.cur.surface == domain.SurfaceAndroid {
			service = d.cur.service
		} else {
			svc, err := m.o.Mobile.ForegroundService(ctx)
			if err != nil {
				return "", false, fmt.Errorf("the draft journey needs an android leg for this action, and its service is not known: %w. Open the leg yourself with semar_journey_add_leg (surface android, service, and the app if its name differs), then run the action again", err)
			}
			service = svc
		}
	}

	cur := d.cur
	if cur == nil || cur.surface != kind || cur.service != service {
		dropped, err := m.closeCurLocked(ctx, d)
		if err != nil {
			return "", false, fmt.Errorf("the open leg could not be closed, so the action did not run: %w", err)
		}
		if dropped != "" {
			notes = append(notes, dropped)
		}
		l := &openLeg{id: d.autoID(kind), surface: kind, service: service, needs: d.previous(), auto: true}
		if err := m.openLegLocked(ctx, d, l); err != nil {
			return "", false, err
		}
		cur = l
		notes = append(notes, fmt.Sprintf("journey: opened the leg %s (%s, service %s)%s", l.id, l.surface, l.service, needsText(l.needs)))
		if l.buildNote != "" {
			notes = append(notes, "journey: warning: "+l.buildNote)
		}
	}
	if kind == domain.SurfaceAndroid {
		was := cur.flowStarted
		if err := m.ensureAndroidLocked(ctx, d, cur); err != nil {
			return "", false, err
		}
		started = cur.flowStarted && !was
	}
	return strings.Join(notes, "\n"), started, nil
}

func needsText(needs []domain.LegID) string {
	if len(needs) == 0 {
		return ""
	}
	ids := make([]string, len(needs))
	for i, n := range needs {
		ids[i] = string(n)
	}
	return ", needs " + strings.Join(ids, ", ")
}

// ensureAndroidLocked starts the recording of the flow of an android leg with the first action of the leg,
// which may be a run of a saved flow: the recording keeps it as a step, and the checks that follow are the
// leg's.
func (m *Manager) ensureAndroidLocked(ctx context.Context, d *draft, l *openLeg) error {
	if l.flowStarted {
		return nil
	}
	if err := m.o.Mobile.StartLegFlow(ctx, l.flowID, l.service, string(d.id)); err != nil {
		return fmt.Errorf("the recording of the android leg %s did not start, so the action did not run: %w", l.id, err)
	}
	l.flowStarted = true
	return nil
}

// ownedByLeg are the tools that end or write a recording. While the draft holds an android leg that is
// recording, the recording is the journey's, so the agent closes it through the journey tools.
var ownedByLeg = map[string]bool{
	"semar_record_start": true, "semar_record_save": true, "semar_record_stop": true, "semar_record_undo": true,
}

// refuseRecordTool refuses a tool that would start, save, or end the recording of an android leg.
func (m *Manager) refuseRecordTool(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.d != nil && m.d.cur != nil && m.d.cur.flowStarted {
		return fmt.Errorf("the android leg %s is being recorded by the draft journey, so %s is refused: go on with the mobile_* tools and semar_record_assert, and close the leg with semar_journey_add_leg or semar_journey_save, or drop it with semar_journey_discard", m.d.cur.id, name)
	}
	return nil
}

// endsWebWindow are the web tools that close the browser, with or without saving what it recorded.
var endsWebWindow = map[string]bool{"web_close": true, "web_save": true}

// refuseWebEnd refuses a tool that would close the browser while the draft holds an open web leg: the
// leg reads the events of that window when it is closed, so the window has to outlive it.
func (m *Manager) refuseWebEnd(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.d != nil && m.d.cur != nil && m.d.cur.surface == domain.SurfaceWeb {
		return fmt.Errorf("%s is refused: cannot close/save the web browser while a journey web leg (%s) is open; call semar_journey_save or semar_journey_discard first", name, m.d.cur.id)
	}
	return nil
}
