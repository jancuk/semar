package webmcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
)

var refPattern = regexp.MustCompile(`^e[0-9]{1,6}$`)

var refSchema = mcp.String("A ref such as e3 from web_list_elements.")

type openArgs struct {
	Service string `json:"service"`
	Path    string `json:"path"`
	URL     string `json:"url"`
	Account string `json:"account"`
	Headed  bool   `json:"headed"`
}

func (m *Manager) tools() []*toolDef {
	return []*toolDef{
		{
			name: "web_open", title: "Open a page", openWorld: true,
			desc: "Open a page of the web service in Chrome, and give the page as a list of elements with refs. The first call starts the browser, on the saved sign-in of the account, and the recording starts there. Later calls move the same window. Give path (below the address of the service) or url, or neither for the start page. The address must be on a host of the environment. While the server only observes, this still works, and the browser then cannot write.",
			schema: mcp.Object(map[string]mcp.Schema{
				"service": mcp.String("The web service, when the environment has more than one."),
				"path":    mcp.String("A path below the address of the service, such as /orders/42."),
				"url":     mcp.String("A full address, instead of path."),
				"account": mcp.String("The account to sign in as, as named in the configuration. The first call only."),
				"headed":  mcp.Bool("Show the browser window. The first call only."),
			}),
			run: m.open,
		},
		{
			name: "web_list_elements", title: "List the elements of the page",
			desc: "List what a person can act on or read on the page, one line per element, each with a ref. A password field is listed with no value. Refs are valid until the next list; list again after the page changes.",
			schema: mcp.Object(map[string]mcp.Schema{
				"format": mcp.Enum("text is a line per element (the default). json is the same facts as JSON.", "text", "json"),
			}),
			run: m.listElements,
		},
		{
			name: "web_click", title: "Click", write: true, openWorld: true,
			desc:   "Click the element that ref names, as a person does with the mouse. It is recorded as a step of the test.",
			schema: mcp.Object(map[string]mcp.Schema{"ref": refSchema}).Required("ref"),
			run:    m.act("clicked", func(ctx context.Context, br Browser, a actionArgs) error { return br.Click(ctx, a.Ref) }),
		},
		{
			name: "web_fill", title: "Type in a field", write: true,
			desc: "Replace what a field holds by typing text at it. To type a password, name an environment variable in secretEnv instead of text: the server types its value, the test keeps only the name as secret: env:NAME, and the value is never shown. Plain text into a password field is refused. One secretEnv per recording.",
			schema: mcp.Object(map[string]mcp.Schema{
				"ref":       refSchema,
				"text":      mcp.String("The text to type. {{run.id}} and the date variables are filled in."),
				"secretEnv": mcp.String("The name of an environment variable whose value is typed into a password field. Give it instead of text."),
			}).Required("ref"),
			run: m.fill,
		},
		{
			name: "web_select", title: "Choose an option", write: true,
			desc: "Choose the option of a list by its label.",
			schema: mcp.Object(map[string]mcp.Schema{
				"ref":   refSchema,
				"label": mcp.String("The label of the option, as the list shows it."),
			}).Required("ref", "label"),
			run: m.act("selected", func(ctx context.Context, br Browser, a actionArgs) error {
				return br.Select(ctx, a.Ref, a.Label)
			}),
		},
		{
			name: "web_set_checked", title: "Check or clear a box", write: true,
			desc: "Check or clear a checkbox. A box that is in the state already is left alone.",
			schema: mcp.Object(map[string]mcp.Schema{
				"ref":     refSchema,
				"checked": mcp.Bool("true to check the box, false to clear it."),
			}).Required("ref", "checked"),
			run: m.act("set", func(ctx context.Context, br Browser, a actionArgs) error {
				if a.Checked == nil {
					return errors.New("give checked: true or false")
				}
				return br.SetChecked(ctx, a.Ref, *a.Checked)
			}),
		},
		{
			name: "web_press", title: "Press a key", write: true,
			desc: "Press Enter or Escape, at the element that ref names, or at the page when there is no ref.",
			schema: mcp.Object(map[string]mcp.Schema{
				"key": mcp.Enum("The key.", "Enter", "Escape"),
				"ref": refSchema,
			}).Required("key"),
			run: m.press,
		},
		{
			name: "web_hover", title: "Hover", write: true,
			desc:   "Hold the pointer over the element that ref names, for a menu or a tip that shows then.",
			schema: mcp.Object(map[string]mcp.Schema{"ref": refSchema}).Required("ref"),
			run:    m.act("hovered", func(ctx context.Context, br Browser, a actionArgs) error { return br.Hover(ctx, a.Ref) }),
		},
		{
			name: "web_drag", title: "Drag", write: true,
			desc: "Drag the element of fromRef onto the element of toRef.",
			schema: mcp.Object(map[string]mcp.Schema{
				"fromRef": mcp.String("The ref of the element to pick up."),
				"toRef":   mcp.String("The ref of the element to drop it on."),
			}).Required("fromRef", "toRef"),
			run: m.drag,
		},
		{
			name: "web_read", title: "Read an element",
			desc:   "Give the text that an element shows, as a check would read it. It records nothing. A password field is never read.",
			schema: mcp.Object(map[string]mcp.Schema{"ref": refSchema}).Required("ref"),
			run:    m.read,
		},
		{
			name: "web_assert", title: "Record a check", write: true,
			desc:   "Record a check that the element shows the text that it shows now, and give that text. The saved test expects it, marked as written by a model. Check what the criterion needs, such as a result or a message, and not what you typed.",
			schema: mcp.Object(map[string]mcp.Schema{"ref": refSchema}).Required("ref"),
			run:    m.assert,
		},
		{
			name: "web_screenshot", title: "Take a screenshot",
			desc: "Take a screenshot of the page as a JPEG image. With saveTo, also save it as a .jpg or .jpeg file in the output folder. Refused when the page shows a secret value.",
			schema: mcp.Object(map[string]mcp.Schema{
				"saveTo": mcp.String("A path of a .jpg or .jpeg file, relative to the output folder of the server."),
			}),
			run: m.screenshot,
		},
		{
			name: "web_save", title: "Save the recording as a test", write: true,
			desc: "End the browser and save what was recorded as a web test: the screens, the scenario, and the script. The test needs at least one web_assert. It is replayed once, and removed when the replay does not end as recorded. The browser is closed either way: open it again to record another test.",
			schema: mcp.Object(map[string]mcp.Schema{
				"id":      mcp.String("The ID of the scenario, such as SHOP-482-07."),
				"ticket":  mcp.String("The ticket of the scenario. The ID with its number cut off when empty."),
				"title":   mcp.String("The title of the scenario."),
				"replace": mcp.Bool("Replace a scenario of the same ID."),
				"verify":  mcp.Bool("Replay the test once and keep it only when it ends as recorded. true by default."),
			}).Required("id"),
			run: m.save,
		},
		{
			name: "web_close", title: "Close the browser",
			desc: "Close the browser. A recording that was not saved is lost, so a recording with steps in it needs discard: true.",
			schema: mcp.Object(map[string]mcp.Schema{
				"discard": mcp.Bool("Close even when the recording has steps that were not saved."),
			}),
			run: m.closeTool,
		},
	}
}

type actionArgs struct {
	Ref     string `json:"ref"`
	Label   string `json:"label"`
	Checked *bool  `json:"checked"`
}

// text is a result of one text block.
func text(format string, args ...any) (mcp.Result, error) {
	return mcp.TextResult(fmt.Sprintf(format, args...)), nil
}

func (m *Manager) browser() (Browser, error) {
	if m.br == nil {
		return nil, errNoBrowser
	}
	return m.br, nil
}

// checkRef refuses a ref that is not of the form of the list, and a page that was not listed since it
// changed, and gives the element of the last list.
func (m *Manager) checkRef(ref string) (pagelist.Element, error) {
	if !refPattern.MatchString(ref) {
		return pagelist.Element{}, fmt.Errorf("%q is not a ref: use one such as e3 from web_list_elements", ref)
	}
	if !m.fresh {
		return pagelist.Element{}, errors.New("the page was not listed since it changed: call web_list_elements, then use a ref of that list")
	}
	e, ok := m.last.Find(ref)
	if !ok {
		return pagelist.Element{}, fmt.Errorf("%s is not in the last list: use a ref of web_list_elements", ref)
	}
	return e, nil
}

// afterAction forgets nothing: the refs stay valid as the window reads them, and the page is read again
// by the next list. It answers in one line.
func after(what, ref string) (mcp.Result, error) {
	return text("%s %s. Call web_list_elements to see the page now.", what, ref)
}

func (m *Manager) open(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a openArgs
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.be == nil {
		return mcp.Result{}, errors.New("this server has no way to open a browser")
	}
	if m.br != nil && a.Service != "" && a.Service != m.info.Service {
		return mcp.Result{}, fmt.Errorf("the browser is on the service %s: call web_close, then open the other service", m.info.Service)
	}
	if m.br != nil && a.Account != "" && a.Account != m.info.Account {
		return mcp.Result{}, fmt.Errorf("the browser is signed in as %s: call web_close, then open it as another account", m.info.Account)
	}
	if m.br != nil {
		a.Service = m.info.Service
	}
	svc, target, err := m.resolve(a)
	if err != nil {
		return mcp.Result{}, err
	}
	if m.br == nil {
		br, info, err := m.be.Open(ctx, OpenRequest{Service: svc.Name, URL: target, Account: a.Account, Headed: a.Headed, ReadOnly: m.guard.Observe})
		if err != nil {
			return mcp.Result{}, err
		}
		if info.Service == "" {
			info.Service = svc.Name
		}
		m.br, m.info, m.savedAt = br, info, br.Mark()
	} else if err := m.br.Open(ctx, target); err != nil {
		return mcp.Result{}, err
	}
	m.last, m.fresh = pagelist.List{}, false
	return m.listText(ctx, "text", "Opened "+target+".\n")
}

func (m *Manager) listElements(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Format string `json:"format"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	format := strings.ToLower(strings.TrimSpace(a.Format))
	if format != "" && format != "text" && format != "json" {
		return mcp.Result{}, errors.New("format must be text or json")
	}
	return m.listText(ctx, format, "")
}

// listText reads the page, keeps the list, and gives it as text or JSON.
func (m *Manager) listText(ctx context.Context, format, lead string) (mcp.Result, error) {
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	l, err := br.List(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	m.last, m.fresh = l, true
	if format == "json" {
		b, err := json.Marshal(listJSON(l))
		if err != nil {
			return mcp.Result{}, fmt.Errorf("the list could not be encoded: %w", err)
		}
		return mcp.TextResult(string(b)), nil
	}
	return mcp.TextResult(lead + l.String()), nil
}

type elementJSON struct {
	Ref      string            `json:"ref"`
	Role     string            `json:"role"`
	Name     string            `json:"name,omitempty"`
	Value    string            `json:"value,omitempty"`
	State    string            `json:"state,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
	Hidden   bool              `json:"hidden,omitempty"`
	Secret   bool              `json:"secret,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Options  []string          `json:"options,omitempty"`
}

func listJSON(l pagelist.List) map[string]any {
	els := make([]elementJSON, len(l.Elements))
	for i, e := range l.Elements {
		els[i] = elementJSON{Ref: e.Ref, Role: e.Role, Name: e.Name, Value: e.Value, State: e.State, Disabled: e.Disabled,
			Hidden: e.Hidden, Secret: e.Secret, Attrs: e.Attrs, Options: e.Options}
	}
	return map[string]any{"url": l.URL, "title": l.Title, "elements": els, "omitted": l.Omitted}
}

// act makes the run function of a tool that does one thing to one ref.
func (m *Manager) act(done string, do func(ctx context.Context, br Browser, a actionArgs) error) func(context.Context, mcp.Call) (mcp.Result, error) {
	return func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
		var a actionArgs
		if err := mcp.DecodeArgs(call.Args, &a); err != nil {
			return mcp.Result{}, err
		}
		br, err := m.browser()
		if err != nil {
			return mcp.Result{}, err
		}
		if _, err := m.checkRef(a.Ref); err != nil {
			return mcp.Result{}, err
		}
		if err := do(ctx, br, a); err != nil {
			return mcp.Result{}, err
		}
		return after(done, a.Ref)
	}
}

func (m *Manager) fill(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Ref       string  `json:"ref"`
		Text      *string `json:"text"`
		SecretEnv string  `json:"secretEnv"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	e, err := m.checkRef(a.Ref)
	if err != nil {
		return mcp.Result{}, err
	}
	switch {
	case a.Text != nil && a.SecretEnv != "":
		return mcp.Result{}, errors.New("give text or secretEnv, not both")
	case a.SecretEnv != "":
		if err := record.CheckSecretEnv(a.SecretEnv); err != nil {
			return mcp.Result{}, err
		}
		if !e.Secret {
			return mcp.Result{}, fmt.Errorf("%s is not a password field, so secretEnv is refused: a secret typed into another field would be kept in the test as plain text", a.Ref)
		}
		if m.secretEnv != "" && m.secretEnv != a.SecretEnv {
			return mcp.Result{}, fmt.Errorf("this recording already types the secret of %s: a recording keeps one secret environment variable", m.secretEnv)
		}
		v, ok := m.env(a.SecretEnv)
		if !ok || v == "" {
			return mcp.Result{}, fmt.Errorf("the environment variable %s is not set", a.SecretEnv)
		}
		// The value is known to the scrubber before it is typed, so that no text can show it.
		m.scrub.Add(v)
		if m.onSecret != nil {
			m.onSecret(v)
		}
		m.noteSecret(v)
		if err := br.Fill(ctx, a.Ref, v); err != nil {
			return mcp.Result{}, err
		}
		m.secretEnv = a.SecretEnv
		return text("typed the value of %s into %s. The value is not shown, and the test keeps only secret: env:%s.", a.SecretEnv, a.Ref, a.SecretEnv)
	case a.Text == nil:
		return mcp.Result{}, errors.New("give the text to type, or secretEnv for a password")
	}
	if e.Secret {
		return mcp.Result{}, fmt.Errorf("%s is a password field: plain text is refused, because a test cannot keep it. Use secretEnv with the name of an environment variable that holds the password", a.Ref)
	}
	for _, s := range m.secrets {
		if strings.Contains(*a.Text, s) {
			return mcp.Result{}, errors.New("the text holds a secret value, which a test would keep in plain text: use secretEnv for a password")
		}
	}
	if err := br.Fill(ctx, a.Ref, *a.Text); err != nil {
		return mcp.Result{}, err
	}
	return after("filled", a.Ref)
}

func (m *Manager) noteSecret(v string) {
	for _, s := range m.secrets {
		if s == v {
			return
		}
	}
	m.secrets = append(m.secrets, v)
}

func (m *Manager) press(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Key string `json:"key"`
		Ref string `json:"ref"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	if a.Key != "Enter" && a.Key != "Escape" {
		return mcp.Result{}, fmt.Errorf("the key %q is not one that a test presses: use Enter or Escape", a.Key)
	}
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	if a.Ref != "" {
		if _, err := m.checkRef(a.Ref); err != nil {
			return mcp.Result{}, err
		}
	} else if !m.fresh {
		return mcp.Result{}, errors.New("the page was not listed since it changed: call web_list_elements first")
	}
	if err := br.Press(ctx, a.Ref, a.Key); err != nil {
		return mcp.Result{}, err
	}
	return text("pressed %s. Call web_list_elements to see the page now.", a.Key)
}

func (m *Manager) drag(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		From string `json:"fromRef"`
		To   string `json:"toRef"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	for _, ref := range []string{a.From, a.To} {
		if _, err := m.checkRef(ref); err != nil {
			return mcp.Result{}, err
		}
	}
	if err := br.Drag(ctx, a.From, a.To); err != nil {
		return mcp.Result{}, err
	}
	return text("dragged %s onto %s. Call web_list_elements to see the page now.", a.From, a.To)
}

func (m *Manager) read(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a actionArgs
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	e, err := m.checkRef(a.Ref)
	if err != nil {
		return mcp.Result{}, err
	}
	if e.Secret {
		return mcp.Result{}, fmt.Errorf("%s is a password field, and a password is never read", a.Ref)
	}
	shown, err := br.Shows(ctx, a.Ref)
	if err != nil {
		return mcp.Result{}, err
	}
	return mcp.TextResult(shown), nil
}

func (m *Manager) assert(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a actionArgs
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	e, err := m.checkRef(a.Ref)
	if err != nil {
		return mcp.Result{}, err
	}
	if e.Secret {
		return mcp.Result{}, fmt.Errorf("%s is a password field, and a password is never read", a.Ref)
	}
	shown, err := br.Check(ctx, a.Ref)
	if err != nil {
		return mcp.Result{}, err
	}
	return text("recorded a check of %s: it shows %q", a.Ref, shown)
}

func (m *Manager) screenshot(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		SaveTo string `json:"saveTo"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	br, err := m.browser()
	if err != nil {
		return mcp.Result{}, err
	}
	exts := []string{".jpg", ".jpeg"}
	path := ""
	if a.SaveTo != "" {
		if m.out == nil {
			return mcp.Result{}, errors.New("this server has no output folder, so it cannot save files")
		}
		if path, err = m.out.Resolve(a.SaveTo, exts); err != nil {
			return mcp.Result{}, fmt.Errorf("saveTo is refused: %w", err)
		}
	}
	// The page is read again, so that the check is on what the picture shows now.
	l, err := br.List(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	m.last, m.fresh = l, true
	if m.shows(l.String()) {
		return mcp.Result{}, errors.New("the page shows a secret value, so no screenshot is taken: a picture cannot be scrubbed")
	}
	data, err := br.Screenshot(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	// A page that changed while the picture was taken is checked once more.
	if l2, err := br.List(ctx); err == nil {
		m.last = l2
		if m.shows(l2.String()) {
			return mcp.Result{}, errors.New("the page shows a secret value, so no screenshot is given: a picture cannot be scrubbed")
		}
	}
	caption := fmt.Sprintf("%s (%d bytes)", l.URL, len(data))
	if path != "" {
		saved, err := m.out.Write(path, exts, data)
		if err != nil {
			return mcp.Result{}, err
		}
		caption += ", saved to " + saved
	}
	return mcp.ImageResult(base64.StdEncoding.EncodeToString(data), "image/jpeg", caption), nil
}

// shows reports whether text holds a secret that this server knows.
func (m *Manager) shows(text string) bool {
	for _, s := range m.secrets {
		if strings.Contains(text, s) {
			return true
		}
	}
	return m.scrub.Leaks(text)
}

// unsaved says whether the window heard anything but a move since the last save.
func (m *Manager) unsaved() bool {
	for _, ev := range m.br.EventsFrom(m.savedAt) {
		if ev.Kind != record.KindOpen {
			return true
		}
	}
	return false
}

func (m *Manager) save(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		ID      string `json:"id"`
		Ticket  string `json:"ticket"`
		Title   string `json:"title"`
		Replace bool   `json:"replace"`
		Verify  *bool  `json:"verify"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	if _, err := m.browser(); err != nil {
		return mcp.Result{}, err
	}
	if err := idcheck.Scenario(a.ID); err != nil {
		return mcp.Result{}, err
	}
	if a.Ticket != "" {
		if err := idcheck.Check("ticket", a.Ticket); err != nil {
			return mcp.Result{}, err
		}
	}
	if m.be == nil {
		return mcp.Result{}, errors.New("this server has no way to write a test")
	}
	verify := a.Verify == nil || *a.Verify
	vars := m.br.Vars()
	info, secretEnv := m.info, m.secretEnv
	// The window ends first: it sends the text of a field that was typed and not yet sent, and the replay
	// then has the profile to itself.
	events := m.closeLocked()
	res, err := m.be.Save(ctx, SaveRequest{
		ID: a.ID, Ticket: a.Ticket, Title: a.Title, Replace: a.Replace, Verify: verify, SecretEnv: secretEnv, Info: info, Client: call.Client.Name,
	}, events, vars)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("nothing was saved, and the browser is closed: %w", err)
	}
	out := map[string]any{
		"id": a.ID, "scenario": res.ScenarioPath, "screens": res.ScreensPath, "actions": res.Actions, "checks": res.Checks,
		"replay": res.Replay, "browser": "closed",
	}
	if len(res.Notes) > 0 {
		out["notes"] = res.Notes
	}
	if len(res.FailsNow) > 0 {
		out["failsNow"] = res.FailsNow
	}
	b, err := json.Marshal(out)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the result could not be encoded: %w", err)
	}
	return mcp.TextResult(string(b)), nil
}

func (m *Manager) closeTool(_ context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Discard bool `json:"discard"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.br == nil {
		return text("no browser was open.")
	}
	if !a.Discard && m.unsaved() {
		return mcp.Result{}, errors.New("the recording has steps that were not saved: call web_save to keep them, or web_close with discard: true to throw them away")
	}
	m.closeLocked()
	return text("the browser is closed.")
}
