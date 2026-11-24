package apimcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/takeover"
)

const (
	// scenarioName is the scenario that the actions of the tools carry.
	scenarioName = "mcp"
	maxCaptures  = 20
	maxExpects   = 20
)

var (
	methodNames = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	captureName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	pathValue   = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
	// shownHeaders are the response headers that a result shows. Set-Cookie and WWW-Authenticate are not
	// among them.
	shownHeaders = []string{"Content-Type", "Location", "ETag", "Last-Modified", "Retry-After", "Link", "X-Request-Id", "Content-Location"}
)

type captureArg struct {
	Name     string `json:"name"`
	JSONPath string `json:"jsonpath"`
}

type expectArg struct {
	Status   *int            `json:"status"`
	JSONPath string          `json:"jsonpath"`
	Header   string          `json:"header"`
	Equals   json.RawMessage `json:"equals"`
	Contains *string         `json:"contains"`
	Exists   *bool           `json:"exists"`
}

type requestArgs struct {
	Service  string          `json:"service"`
	Method   string          `json:"method"`
	Path     string          `json:"path"`
	Query    mcp.StringMap   `json:"query"`
	JSON     json.RawMessage `json:"json"`
	Headers  mcp.StringMap   `json:"headers"`
	As       string          `json:"as"`
	Captures []captureArg    `json:"captures"`
	Expect   []expectArg     `json:"expect"`
	Why      string          `json:"why"`
	MaxBody  int             `json:"maxBody"`
}

type expectOut struct {
	Index    int    `json:"index"`
	Check    string `json:"check"`
	State    string `json:"state"`
	Expected string `json:"expected,omitempty"`
	Observed string `json:"observed,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Recorded bool   `json:"recorded"`
}

type requestOut struct {
	Service       string            `json:"service"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Sent          bool              `json:"sent"`
	Status        int               `json:"status,omitempty"`
	Refused       string            `json:"refused,omitempty"`
	Error         string            `json:"error,omitempty"`
	EffectUnknown bool              `json:"effectUnknown,omitempty"`
	MS            int64             `json:"ms"`
	Headers       map[string]string `json:"headers,omitempty"`
	Body          any               `json:"body,omitempty"`
	BodyBytes     int               `json:"bodyBytes,omitempty"`
	BodyCut       bool              `json:"bodyCut,omitempty"`
	Doc           *domain.DocCheck  `json:"doc,omitempty"`
	Captured      map[string]string `json:"captured,omitempty"`
	Missing       []string          `json:"missingCaptures,omitempty"`
	Expect        []expectOut       `json:"expect,omitempty"`
	Lifted        []string          `json:"lifted,omitempty"`
	Step          *int              `json:"step,omitempty"`
}

func (m *Manager) requestTool() mcp.Tool {
	title := "Send an API request"
	return mcp.Tool{
		Name: "api_request", Title: title,
		Description: "Send one HTTP request to a service of the environment and give the stored answer: status, a few headers, the body (cut at maxBody), the verdict of the OpenAPI document, the values captured, and each expect judged by code. " +
			"The effects policy applies: hosts, methods, the deny list, and read-only mode. Reads (GET, HEAD) always work. A write needs why, is refused while the server only observes, is never sent when it matches effects.once, and is refused while a person is in control. " +
			"path, query values, header values, and string values of json may hold {{name}} variables from earlier captures. as names an auth profile from api_list_services: tokens are never passed by hand and headers that look like credentials are refused. " +
			"What you send is logged as a template, so a journey can be written from it.",
		Input: mcp.Object(map[string]mcp.Schema{
			"service": mcp.String("The service name from api_list_services."),
			"method":  mcp.Enum("The HTTP method.", methodNames...),
			"path":    mcp.String("The path, starting with /, without a query. It may hold {{name}} variables, such as /v1/orders/{{order_id}}."),
			"query":   mcp.NameValue("Query parameters, as a list of {name, value}."),
			"json":    mcp.String("The JSON body, written as JSON text, such as {\"amount\": 100}. String values may hold {{name}} variables."),
			"headers": mcp.NameValue("Extra headers, as a list of {name, value}: Accept, Content-Type, If-Match, If-None-Match, Idempotency-Key, X-Request-Id, and other X- headers. Credentials go through as."),
			"as":      mcp.String("The auth profile to sign in as, from api_list_services."),
			"captures": mcp.Array("Values to keep from a JSON answer for later requests, which name them as {{name}}.", mcp.Object(map[string]mcp.Schema{
				"name":     mcp.String("The variable name: letters, digits, and _, starting with a letter."),
				"jsonpath": mcp.String("A JSONPath that selects one string, number, or boolean, such as $.data.id."),
			}).Required("name", "jsonpath")),
			"expect": mcp.Array("Checks that code judges on the answer. Give exactly one of status, jsonpath, or header in each. A check that holds is recorded for the journey.", mcp.Object(map[string]mcp.Schema{
				"status":   mcp.Int("The expected status."),
				"jsonpath": mcp.String("A JSONPath into the body, with one of equals, contains, or exists."),
				"header":   mcp.String("A response header name, with one of equals, contains, or exists."),
				"equals":   mcp.String("The expected value. A number, true, false, or null written as such is compared as that JSON value; put it in quotes to compare text. It may be a {{name}} variable."),
				"contains": mcp.String("A text that the value holds."),
				"exists":   mcp.Bool("Whether the JSONPath or header is there."),
			})),
			"why":     mcp.String("Why the request is sent, in at most 200 characters. Required for a write.").With("maxLength", MaxWhy),
			"maxBody": mcp.Int("Most bytes of the answer body to give, up to 262144. 32768 by default.").With("minimum", 1).With("maximum", MaxBody),
		}).Required("service", "method", "path"),
		// A request is a read or a write by its method and path, so the tool is not marked as a write: the
		// guard middleware would then refuse every GET while the server observes. The handler asks the
		// guard and the baton itself, before it sends a write.
		Annotations: mcp.Annotations{Title: title, ReadOnly: mcp.BoolPtr(false), Destructive: mcp.BoolPtr(true), OpenWorld: mcp.BoolPtr(true)},
		Surface:     string(mcpkit.SurfaceAPI), Write: false,
		Handler: m.request,
	}
}

func refuse(format string, args ...any) mcp.Result {
	return mcp.ErrorResult(fmt.Sprintf(format, args...))
}

func (m *Manager) refuseScrubbed(err error) mcp.Result { return mcp.ErrorResult(m.scrub(err.Error())) }

func (m *Manager) request(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a requestArgs
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	m.callMu.Lock()
	defer m.callMu.Unlock()

	svc, ok := m.service(a.Service)
	if !ok {
		return m.refuseScrubbed(fmt.Errorf("there is no service %q; the services are %s", a.Service, m.serviceNames())), nil
	}
	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if !slices.Contains(methodNames, method) {
		return refuse("method %q is not one of %s", a.Method, strings.Join(methodNames, ", ")), nil
	}
	a.Why = strings.TrimSpace(a.Why)
	if utf8.RuneCountInString(a.Why) > MaxWhy {
		return refuse("why is longer than %d characters", MaxWhy), nil
	}
	maxBody := a.MaxBody
	switch {
	case maxBody == 0:
		maxBody = DefaultBody
	case maxBody < 0 || maxBody > MaxBody:
		return refuse("maxBody must be from 1 to %d", MaxBody), nil
	}
	if a.As != "" && !slices.Contains(m.o.Profiles, a.As) {
		return m.refuseScrubbed(fmt.Errorf("there is no auth profile %q; the profiles are %s", a.As, listOrNone(m.o.Profiles))), nil
	}
	if err := CheckHeaders(a.Headers); err != nil {
		return m.refuseScrubbed(err), nil
	}
	captures, err := toCaptures(a.Captures)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}
	checks, err := toChecks(a.Expect)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}
	body, err := parseJSONArg(a.JSON)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}

	tpl := domain.APIRequest{Service: svc.Name, Method: method, Path: a.Path, Query: map[string]string(a.Query), Headers: map[string]string(a.Headers), JSON: body}
	vars := m.Vars()
	if m.o.RunID != nil && strings.Contains(templateText(tpl), "{{") {
		if id, err := m.o.RunID(ctx); err == nil && id != "" {
			vars["run.id"] = id
		}
	}
	rendered, err := script.Render(tpl, vars)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}
	if m.o.Policy == nil {
		return refuse("the environment has no effects policy, so no request can be judged"), nil
	}
	u, err := api.JoinURL(svc.BaseURL, rendered.Path, rendered.Query)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}
	preq := effects.Request{Method: method, URL: u}
	write := m.o.Policy.Classify(preq) == effects.Write

	if write {
		if a.Why == "" {
			return refuse("a write needs why: say in a few words why this %s is sent", method), nil
		}
		if err := m.o.Guard.Allow(mcpkit.SurfaceAPI, true); err != nil {
			return m.refuseScrubbed(err), nil
		}
		if m.o.Policy.Once(preq) {
			return refuse("refused: %s matches effects.once in semar.yaml, which means once in the life of the environment, and a tool never sends it", method), nil
		}
		if m.o.Baton != nil {
			ctx2, leave, err := m.o.Baton.Enter(ctx, takeover.Agent, true)
			if err != nil {
				var he *takeover.ErrHumanInControl
				if errors.As(err, &he) {
					return humanInControl(he), nil
				}
				return m.refuseScrubbed(err), nil
			}
			defer leave(true, "")
			ctx = ctx2
		}
	}

	logged, lifted := lift(tpl, vars)
	backend, err := m.backend(ctx)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}
	jsonBytes, err := encodeJSON(rendered.JSON)
	if err != nil {
		return m.refuseScrubbed(err), nil
	}

	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	by := m.o.By
	if by == "" {
		by = domain.ActorAgent
	}
	res, err := backend.Do(ctx, api.Call{
		Use:     domain.UseCase{Pass: domain.PassExplore, Scenario: scenarioName, Step: domain.StepID(fmt.Sprintf("r-%d", n))},
		By:      by,
		Service: svc.Name, Method: method, Path: rendered.Path, PathTemplate: logged.Path,
		Query: rendered.Query, Headers: rendered.Headers, JSON: jsonBytes,
		AuthProfile: a.As, Why: a.Why, Captures: captures,
	})
	if err != nil {
		return m.refuseScrubbed(err), nil
	}
	return m.finish(res, a, logged, lifted, captures, checks, vars, write, maxBody)
}

func (m *Manager) backend(ctx context.Context) (RequestBackend, error) {
	if m.o.Backend == nil {
		return nil, errors.New("the API engine is not available")
	}
	return m.o.Backend(ctx)
}

// finish builds the result of a request that reached the engine, and logs the step when it was sent.
func (m *Manager) finish(res api.Result, a requestArgs, logged domain.APIRequest, lifted []string, captures []domain.Capture, checks []domain.Check, vars map[string]string, write bool, maxBody int) (mcp.Result, error) {
	act := res.Action
	out := requestOut{Service: logged.Service, Method: logged.Method, Path: logged.Path, Refused: m.scrub(act.Refused), Error: m.scrub(act.Err), EffectUnknown: act.EffectUnknown, MS: act.DurationMS, Doc: act.Doc, Lifted: lifted}
	sent := act.Response != nil && act.Refused == "" && act.Err == ""
	out.Sent = sent
	if act.Response != nil {
		r := act.Response
		out.Status = r.Status
		out.Headers = m.pickHeaders(r.Headers)
		out.BodyBytes = len(r.Body)
		out.Body, out.BodyCut = m.bodyOut(r.Body, r.Truncated, maxBody)
	}
	if len(res.Captured) > 0 {
		out.Captured = map[string]string{}
		for _, k := range slices.Sorted(maps.Keys(res.Captured)) {
			v := act.Captures[k]
			if v == "" {
				v = res.Captured[k]
			}
			out.Captured[k] = m.scrub(v)
		}
	}
	out.Missing = res.Missing

	scope := maps.Clone(vars)
	maps.Copy(scope, res.Captured)
	var recorded []domain.Expectation
	for i, c := range checks {
		eo := expectOut{Index: i, Check: m.scrub(check.Describe(c))}
		if !sent {
			eo.State, eo.Detail = string(domain.CheckNotEvaluated), "the request was not sent"
		} else {
			er := flow.EvalExpect(c, act, scope, scope)
			eo.State, eo.Expected, eo.Observed, eo.Detail = string(er.State), m.scrub(er.Expected), m.scrub(er.Observed), m.scrub(er.Detail)
			if er.State == domain.CheckTrue {
				eo.Recorded = true
				recorded = append(recorded, domain.Expectation{Check: c, Source: domain.SourceBaseline, By: "model"})
			}
		}
		out.Expect = append(out.Expect, eo)
	}

	if sent {
		before := map[string]prior{}
		m.mu.Lock()
		for k, v := range res.Captured {
			old, had := m.vars[k]
			before[k] = prior{old, had}
			m.vars[k] = v
		}
		m.steps = append(m.steps, entry{before: before, step: APIStep{
			Service: logged.Service, Request: logged, Captures: captures, Expect: recorded,
			As: a.As, Status: act.Response.Status, Write: write, Action: a.Why, PathTemplate: logged.Path,
		}})
		idx := len(m.steps)
		m.mu.Unlock()
		out.Step = &idx
	}
	r, err := m.textJSON(out)
	if err != nil {
		return r, err
	}
	r.IsError = !sent
	return r, nil
}

func (m *Manager) pickHeaders(h map[string][]string) map[string]string {
	out := map[string]string{}
	for _, name := range shownHeaders {
		if v := h[http.CanonicalHeaderKey(name)]; len(v) > 0 {
			out[name] = m.scrub(strings.Join(v, ", "))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// bodyOut gives the body as JSON when it is whole and valid, and as text otherwise, cut at limit bytes.
func (m *Manager) bodyOut(b []byte, truncated bool, limit int) (any, bool) {
	if len(b) == 0 {
		return nil, false
	}
	if !utf8.Valid(b) {
		return fmt.Sprintf("[binary, %d bytes]", len(b)), false
	}
	cut := truncated
	if len(b) > limit {
		b, cut = b[:limit], true
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	text := m.scrub(string(b))
	if !cut && json.Valid([]byte(text)) {
		return json.RawMessage(text), false
	}
	return text, cut
}

func humanInControl(e *takeover.ErrHumanInControl) mcp.Result {
	text := "A person is in control, so this request was not sent."
	if e.Reason != "" {
		text += " Reason: " + e.Reason + "."
	}
	text += " Reads still work. Call semar_wait_for_control to wait for the hand-back."
	return mcp.Result{
		Content: []mcp.Content{{Type: "text", Text: text}},
		IsError: true,
		Structured: map[string]any{
			"status": "human_in_control", "reason": e.Reason,
			"since": e.Since.UTC().Format(time.RFC3339), "next": "semar_wait_for_control",
		},
	}
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func toCaptures(in []captureArg) ([]domain.Capture, error) {
	if len(in) > maxCaptures {
		return nil, fmt.Errorf("at most %d captures", maxCaptures)
	}
	var out []domain.Capture
	seen := map[string]bool{}
	for i, c := range in {
		switch {
		case !captureName.MatchString(c.Name):
			return nil, fmt.Errorf("captures[%d]: the name %q must be letters, digits, and _, starting with a letter", i, c.Name)
		case seen[c.Name]:
			return nil, fmt.Errorf("captures[%d]: the name %q appears twice", i, c.Name)
		case strings.TrimSpace(c.JSONPath) == "":
			return nil, fmt.Errorf("captures[%d]: jsonpath is empty", i)
		}
		seen[c.Name] = true
		out = append(out, domain.Capture{Name: c.Name, JSONPath: c.JSONPath})
	}
	return out, nil
}

func toChecks(in []expectArg) ([]domain.Check, error) {
	if len(in) > maxExpects {
		return nil, fmt.Errorf("at most %d expect items", maxExpects)
	}
	var out []domain.Check
	for i, e := range in {
		var c domain.Check
		subjects := 0
		if e.Status != nil {
			subjects++
			c.Status = e.Status
		}
		if e.JSONPath != "" {
			subjects++
			c.JSONPath = e.JSONPath
		}
		if e.Header != "" {
			subjects++
			c.Header = e.Header
		}
		if subjects != 1 {
			return nil, fmt.Errorf("expect[%d]: give exactly one of status, jsonpath, or header", i)
		}
		ops := 0
		if t := bytes.TrimSpace(e.Equals); len(t) > 0 && string(t) != "null" {
			ops++
			v, err := parseOperand(e.Equals)
			if err != nil {
				return nil, fmt.Errorf("expect[%d]: equals: %w", i, err)
			}
			c.Equals = v
		}
		if e.Contains != nil {
			ops++
			c.Contains = *e.Contains
		}
		if e.Exists != nil {
			ops++
			c.Exists = e.Exists
		}
		switch {
		case c.Status != nil && ops > 0:
			return nil, fmt.Errorf("expect[%d]: a status check takes no equals, contains, or exists", i)
		case c.Status == nil && ops != 1:
			return nil, fmt.Errorf("expect[%d]: a jsonpath or header check takes exactly one of equals, contains, or exists", i)
		}
		out = append(out, c)
	}
	return out, nil
}

// parseOperand reads the equals of an expect. A JSON string is text, and text that is a JSON number,
// true, false, or null is that value; text in quotes inside the text stays text.
func parseOperand(raw json.RawMessage) (any, error) {
	var any1 any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&any1); err != nil {
		return nil, errors.New("is not a JSON value")
	}
	s, isString := any1.(string)
	if !isString {
		switch any1.(type) {
		case json.Number, bool, nil:
			return any1, nil
		}
		return nil, errors.New("must be a string, a number, or a boolean")
	}
	var inner any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if err := d.Decode(&inner); err == nil && !d.More() {
		switch v := inner.(type) {
		case json.Number, bool, nil:
			return v, nil
		case string:
			return v, nil // "\"123\"" is the text 123
		}
	}
	return s, nil
}

// parseJSONArg reads the json parameter: JSON text, or a JSON value that the client sent as it is.
func parseJSONArg(raw json.RawMessage) (any, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return nil, nil
	}
	if t[0] == '"' {
		var text string
		if err := json.Unmarshal(t, &text); err != nil {
			return nil, errors.New("json is not valid")
		}
		t = bytes.TrimSpace([]byte(text))
		if len(t) == 0 {
			return nil, nil
		}
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errors.New("json is not valid JSON text")
	}
	if dec.More() {
		return nil, errors.New("json holds more than one JSON value")
	}
	return v, nil
}

func encodeJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, errors.New("the json body cannot be encoded")
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// templateText is the text of the request that may hold variables, for a quick look.
func templateText(r domain.APIRequest) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// lift replaces a literal that equals a known value (4 characters or more) with its {{name}}, in the path
// segments and the string values of the JSON body. It returns a copy and one note for each change.
func lift(r domain.APIRequest, vars map[string]string) (domain.APIRequest, []string) {
	var names []string
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if name != "run.id" && len(vars[name]) >= MinLift {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return r, nil
	}
	find := func(lit string) (string, bool) {
		if strings.Contains(lit, "{{") {
			return "", false
		}
		for _, n := range names {
			if vars[n] == lit {
				return n, true
			}
		}
		return "", false
	}
	var notes []string
	segs := strings.Split(r.Path, "/")
	for i, s := range segs {
		if n, ok := find(s); ok && pathValue.MatchString(s) && s != "." && s != ".." {
			segs[i] = "{{" + n + "}}"
			notes = append(notes, fmt.Sprintf("path segment %d is logged as {{%s}}", i, n))
		}
	}
	r.Path = strings.Join(segs, "/")
	var walk func(where string, v any) any
	walk = func(where string, v any) any {
		switch x := v.(type) {
		case string:
			if n, ok := find(x); ok {
				notes = append(notes, fmt.Sprintf("json %s is logged as {{%s}}", where, n))
				return "{{" + n + "}}"
			}
		case map[string]any:
			out := make(map[string]any, len(x))
			for _, k := range slices.Sorted(maps.Keys(x)) {
				out[k] = walk(where+"."+k, x[k])
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = walk(fmt.Sprintf("%s[%d]", where, i), e)
			}
			return out
		}
		return v
	}
	if r.JSON != nil {
		r.JSON = walk("$", r.JSON)
	}
	return r, notes
}
