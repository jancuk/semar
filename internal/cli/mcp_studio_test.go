package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/localhttp"
)

// studioURL waits for the line of standard error that tells where the studio is and with which token.
var studioLine = regexp.MustCompile(`semar mcp: studio at (http://127\.0\.0\.1:\d+)/#token=([0-9a-f]{64})`)

// studioClient talks to the live studio of a semar mcp the way the page does.
type studioClient struct {
	t     *testing.T
	base  string
	token string
	seen  syncBuffer // every answer, for a search for a secret
}

func waitStudio(t *testing.T, p *mcpProc) *studioClient {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if m := studioLine.FindStringSubmatch(p.stderr.String()); m != nil {
			return &studioClient{t: t, base: m[1], token: m[2]}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no studio address on standard error:\n%s", p.stderr)
	return nil
}

func (c *studioClient) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	} else if method != "GET" {
		rd = strings.NewReader("{}")
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set(localhttp.TokenHeader, c.token)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	_, _ = c.seen.Write(data)
	return res.StatusCode, data
}

type liveEvent struct {
	Seq     uint64 `json:"seq"`
	Who     string `json:"who"`
	Tool    string `json:"tool"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
}

type liveState struct {
	Version     uint64 `json:"version"`
	Owner       string `json:"owner"`
	Pending     bool   `json:"pending"`
	Asked       string `json:"asked"`
	Reason      string `json:"reason"`
	Note        string `json:"note"`
	Mode        string `json:"mode"`
	RecordHuman bool   `json:"recordHuman"`
	Gap         bool   `json:"gap"`
	Observe     bool   `json:"observe"`
	Secret      bool   `json:"secret"`
	Width       int    `json:"width"`
	Windows     []struct {
		Kind     string `json:"kind"`
		Elements []struct {
			Index int    `json:"index"`
			Text  string `json:"text"`
			Desc  string `json:"desc"`
		} `json:"elements"`
	} `json:"windows"`
	Steps []struct {
		N    int    `json:"n"`
		Text string `json:"text"`
		Who  string `json:"who"`
	} `json:"steps"`
	Events []liveEvent `json:"events"`
}

func (c *studioClient) state(path string) liveState {
	c.t.Helper()
	code, data := c.do("GET", path, nil)
	if code != 200 {
		c.t.Fatalf("GET %s = %d %s", path, code, data)
	}
	var st liveState
	if err := json.Unmarshal(data, &st); err != nil {
		c.t.Fatalf("not a state: %v\n%s", err, data)
	}
	return st
}

func (c *studioClient) post(path string, body any) (int, liveState, []byte) {
	c.t.Helper()
	code, data := c.do("POST", path, body)
	var st liveState
	_ = json.Unmarshal(data, &st)
	return code, st, data
}

// callResult runs a tool and gives the whole result, so that a test reads structuredContent too.
func (p *mcpProc) callResult(name string, args map[string]any) map[string]any {
	p.t.Helper()
	return p.request("tools/call", map[string]any{"name": name, "arguments": args})
}

func textOfResult(res map[string]any) string {
	var b strings.Builder
	cs, _ := res["content"].([]any)
	for _, c := range cs {
		if m, ok := c.(map[string]any); ok && m["type"] == "text" {
			b.WriteString(m["text"].(string))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestMCPStudioPauseActResumeAndRequestHuman(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--studio=127.0.0.1:0")
	p.initialize()
	st := waitStudio(t, p)

	names := p.toolNames()
	for _, want := range []string{"semar_control_status", "semar_request_human", "semar_wait_for_control", "mobile_click_on_screen_at_coordinates"} {
		if !hasTool(names, want) {
			t.Errorf("the tool %s is missing: %v", want, names)
		}
	}

	// The studio and the tool server do not share a token, and the token is on standard error only.
	if strings.Count(p.stderr.String(), st.token) != 1 {
		t.Errorf("the studio token must be printed once, with the address:\n%s", p.stderr)
	}
	if code, _ := (&studioClient{t: t, base: st.base, token: strings.Repeat("0", 64)}).do("GET", "/api/live/state", nil); code != http.StatusForbidden {
		t.Errorf("a wrong token = %d, want 403", code)
	}

	first := st.state("/api/live/state")
	if first.Owner != "agent" || first.Mode != "live" || !first.RecordHuman {
		t.Fatalf("state = %+v", first)
	}

	// The agent lists the screen, and the long poll of the studio sees the call.
	type polled struct {
		st liveState
	}
	got := make(chan polled, 1)
	go func() {
		// A poll may wake for a change that is not a call, such as the mode being set: ask again from there.
		since := first.Version
		for i := 0; i < 20; i++ {
			s := st.state(fmt.Sprintf("/api/live/state?since=%d&wait=20", since))
			if len(s.Events) > 0 {
				got <- polled{s}
				return
			}
			since = s.Version
		}
		got <- polled{}
	}()
	time.Sleep(100 * time.Millisecond)
	listing, isErr := p.call("mobile_list_elements_on_screen", map[string]any{})
	if isErr {
		t.Fatalf("list: %s\nstderr:\n%s", listing, p.stderr)
	}
	select {
	case a := <-got:
		var seen bool
		for _, e := range a.st.Events {
			if e.Tool == "mobile_list_elements_on_screen" && e.Who == "agent" && e.OK {
				seen = true
			}
		}
		if !seen {
			t.Errorf("the long poll woke without the call of the agent: %+v", a.st.Events)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the long poll of the studio did not see the call of the agent")
	}
	if status := p.callResult("semar_control_status", nil); !strings.Contains(textOfResult(status), `"owner":"agent"`) {
		t.Errorf("status = %v", status)
	}

	// Pause: the agent's next write is refused at once, and a read still works.
	code, paused, body := st.post("/api/live/pause", map[string]string{"reason": "let me look"})
	if code != 200 || paused.Owner != "human" || paused.Reason != "let me look" {
		t.Fatalf("pause = %d %s", code, body)
	}
	res := p.callResult("mobile_click_on_screen_at_coordinates", map[string]any{"x": 540, "y": 960})
	if res["isError"] != true {
		t.Fatalf("a write while a person holds control went through: %v", res)
	}
	structured, _ := res["structuredContent"].(map[string]any)
	if structured["status"] != "human_in_control" || structured["next"] != "semar_wait_for_control" || structured["reason"] != "let me look" {
		t.Errorf("structuredContent = %v", structured)
	}
	if !strings.Contains(textOfResult(res), "person is in control") {
		t.Errorf("text = %s", textOfResult(res))
	}
	if r.taps.Load() != 0 {
		t.Fatalf("the agent tapped %d times while paused", r.taps.Load())
	}
	if text, isErr := p.call("mobile_list_elements_on_screen", map[string]any{}); isErr {
		t.Errorf("a read was refused while paused: %s", text)
	}

	// The person taps the button: it reaches the device, and the feed says what they did.
	code, afterAct, body := st.post("/api/live/act", map[string]any{"do": "tap", "x": 540, "y": 960})
	if code != 200 {
		t.Fatalf("act = %d %s", code, body)
	}
	if r.taps.Load() != 1 {
		t.Errorf("the studio tap reached the device %d times, want 1", r.taps.Load())
	}
	var human bool
	for _, e := range afterAct.Events {
		if e.Who == "human" && strings.Contains(e.Tool, "tap") {
			human = true
		}
	}
	for _, e := range st.state("/api/live/state?since=0&wait=0").Events {
		if e.Who == "human" && strings.Contains(e.Tool, "tap") {
			human = true
		}
	}
	if !human {
		t.Error("the feed has no line for the tap of the person")
	}

	// Hand back with a note: the agent's next call carries the notice, and sees the screen as it is now.
	code, back, body := st.post("/api/live/resume", map[string]string{"note": "I signed in for you"})
	if code != 200 || back.Owner != "agent" {
		t.Fatalf("resume = %d %s", code, body)
	}
	text, isErr := p.call("mobile_list_elements_on_screen", map[string]any{})
	if isErr {
		t.Fatalf("list after the hand-back: %s", text)
	}
	for _, want := range []string{"a person had control", "I signed in for you", "the screen may have changed", "Hi Ana"} {
		if !strings.Contains(text, want) {
			t.Errorf("the first call after the hand-back lacks %q:\n%s", want, text)
		}
	}
	if text2, _ := p.call("mobile_list_elements_on_screen", map[string]any{}); strings.Contains(text2, "a person had control") {
		t.Error("the notice came twice")
	}
	if text, isErr = p.call("mobile_click_on_screen_at_coordinates", map[string]any{"x": 540, "y": 960}); isErr {
		t.Errorf("the agent cannot act after the hand-back: %s", text)
	}
	if r.taps.Load() != 2 {
		t.Errorf("taps = %d, want 2", r.taps.Load())
	}

	// semar_request_human blocks until the studio hands back, then gives the note and a fresh listing.
	type answer struct {
		res map[string]any
	}
	done := make(chan answer, 1)
	go func() {
		done <- answer{p.callResult("semar_request_human", map[string]any{"reason": "enter the code from the SMS", "timeoutSec": 120})}
	}()
	var asked liveState
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		asked = st.state("/api/live/state?wait=0")
		if asked.Owner == "human" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if asked.Owner != "human" || asked.Asked != "enter the code from the SMS" {
		t.Fatalf("the agent did not ask for help: %+v", asked)
	}
	select {
	case a := <-done:
		t.Fatalf("semar_request_human returned before the hand-back: %v", a.res)
	case <-time.After(300 * time.Millisecond):
	}
	// The agent is blocked, but the studio can still see the screen and act.
	if code, _, body := st.post("/api/live/act", map[string]any{"do": "press", "key": "BACK"}); code != 200 {
		t.Errorf("act while the agent waits = %d %s", code, body)
	}
	if code, _, body := st.post("/api/live/resume", map[string]string{"note": "code entered: done"}); code != 200 {
		t.Fatalf("resume = %d %s", code, body)
	}
	select {
	case a := <-done:
		text := textOfResult(a.res)
		if a.res["isError"] == true {
			t.Fatalf("semar_request_human failed: %s", text)
		}
		for _, want := range []string{`"status":"control_returned"`, "code entered: done", "Hi Ana", "e1"} {
			if !strings.Contains(text, want) {
				t.Errorf("semar_request_human result lacks %q:\n%s", want, text)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("semar_request_human did not return after the hand-back")
	}

	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

func TestMCPStudioObserveModeLetsAPersonPauseButNotAct(t *testing.T) {
	r := newAndroidRig(t, androidOptions{production: true})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--studio=127.0.0.1:0")
	p.initialize()
	st := waitStudio(t, p)

	state := st.state("/api/live/state")
	if !state.Observe {
		// The first read of the screen may come with the first poll after a change.
		state = st.state("/api/live/state?wait=0")
	}
	if code, paused, body := st.post("/api/live/pause", map[string]string{"reason": "look"}); code != 200 || paused.Owner != "human" {
		t.Fatalf("pause in observe mode = %d %s", code, body)
	}
	code, _, body := st.post("/api/live/act", map[string]any{"do": "tap", "x": 540, "y": 960})
	if code != http.StatusForbidden || !strings.Contains(string(body), "production") {
		t.Errorf("act in observe mode = %d %s, want 403 that says why", code, body)
	}
	if r.taps.Load() != 0 {
		t.Errorf("the device was tapped %d times in observe mode", r.taps.Load())
	}
	if !st.state("/api/live/state?wait=0").Observe {
		t.Error("the state does not say that the server only observes")
	}
	st.post("/api/live/resume", map[string]string{"note": ""})
	// The agent's own writes are refused by the guard, and nothing reached the device.
	if text, isErr := p.call("mobile_click_on_screen_at_coordinates", map[string]any{"x": 540, "y": 960}); !isErr || !strings.Contains(text, "production") {
		t.Errorf("isError=%v, %s", isErr, text)
	}
	if r.taps.Load() != 0 {
		t.Errorf("taps = %d", r.taps.Load())
	}
	// Asking a person for help is still allowed: it changes nothing on the device.
	done := make(chan map[string]any, 1)
	go func() {
		done <- p.callResult("semar_request_human", map[string]any{"reason": "look at it", "timeoutSec": 60})
	}()
	deadline := time.Now().Add(15 * time.Second)
	for st.state("/api/live/state?wait=0").Owner != "human" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	st.post("/api/live/resume", map[string]string{"note": "seen"})
	select {
	case res := <-done:
		if res["isError"] == true || !strings.Contains(textOfResult(res), "seen") {
			t.Errorf("request_human = %s", textOfResult(res))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("request_human did not return")
	}
	p.finish()
}

func TestMCPStudioHumanActionsInARecordingAreStepsOrAGap(t *testing.T) {
	r, _ := recordRig(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--studio=127.0.0.1:0")
	p.initialize()
	st := waitStudio(t, p)

	if text, isErr := p.call("semar_record_start", map[string]any{"kind": "flow", "id": "by-hand"}); isErr {
		t.Fatalf("start: %s\nstderr:\n%s", text, p.stderr)
	}
	if code, _, body := st.post("/api/live/pause", nil); code != 200 {
		t.Fatalf("pause = %d %s", code, body)
	}
	cur := st.state("/api/live/state?wait=0")
	if cur.Mode != "record" || !cur.RecordHuman || len(cur.Steps) == 0 {
		t.Fatalf("state = %+v", cur)
	}
	base := len(cur.Steps)

	// Recorded by default: the tap on the button is a step, and the page says whose.
	code, after, body := st.post("/api/live/act", map[string]any{"do": "tap", "x": 540, "y": 960})
	if code != 200 {
		t.Fatalf("act = %d %s", code, body)
	}
	if len(after.Steps) != base+1 || after.Steps[base].Who != "human" || after.Gap {
		t.Errorf("steps = %+v, gap = %v", after.Steps, after.Gap)
	}

	// With "Record my actions" off the same kind of action leaves a gap and no step.
	if code, _, body := st.post("/api/live/record", map[string]bool{"on": false}); code != 200 {
		t.Fatalf("record = %d %s", code, body)
	}
	code, after, body = st.post("/api/live/act", map[string]any{"do": "press", "key": "BACK"})
	if code != 200 {
		t.Fatalf("act = %d %s", code, body)
	}
	if len(after.Steps) != base+1 || !after.Gap {
		t.Errorf("steps = %d, gap = %v, want %d steps and a gap", len(after.Steps), after.Gap, base+1)
	}
	st.post("/api/live/resume", map[string]string{"note": "done by hand"})

	text, isErr := p.call("semar_record_save", map[string]any{"verify": false})
	if !isErr || !strings.Contains(text, "were not recorded") {
		t.Errorf("a save with a gap: isError=%v, %s", isErr, text)
	}
	if fileIn(r, ".semar/flows/by-hand.yaml") {
		t.Error("a save with a gap wrote the flow")
	}
	status, _ := p.call("semar_control_status", map[string]any{})
	if !strings.Contains(status, `"gap":true`) || !strings.Contains(status, `"mode":"record"`) {
		t.Errorf("status = %s", status)
	}
	p.finish()
}

func TestMCPStudioTokenIsItsOwnAndOnStandardErrorOnly(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	const secret = "typed-by-a-person-canary-9931"
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--studio=127.0.0.1:0", "--http", "127.0.0.1:0")
	st := waitStudio(t, p)
	var mcpToken string
	deadline := time.Now().Add(20 * time.Second)
	for mcpToken == "" && time.Now().Before(deadline) {
		if m := regexp.MustCompile(`semar mcp: token: ([0-9a-f]+)`).FindStringSubmatch(p.stderr.String()); m != nil {
			mcpToken = m[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mcpToken == "" || mcpToken == st.token {
		t.Fatalf("the tokens must be two: mcp %q, studio %q\n%s", mcpToken, st.token, p.stderr)
	}
	// The token of the tool server opens nothing of the studio, and the other way round.
	bad := &studioClient{t: t, base: st.base, token: mcpToken}
	if code, _ := bad.do("GET", "/api/live/state", nil); code != http.StatusForbidden {
		t.Errorf("the token of the tool server opened the studio: %d", code)
	}
	// Typed text is in no answer of the studio.
	st.post("/api/live/pause", nil)
	st.post("/api/live/act", map[string]any{"do": "type", "text": secret})
	st.state("/api/live/state?since=0&wait=0")
	st.post("/api/live/resume", map[string]string{"note": "typed something"})
	if strings.Contains(st.seen.String(), secret) {
		t.Errorf("the typed text is in an answer of the studio:\n%s", st.seen.String())
	}
	if strings.Contains(p.stderr.String(), secret) {
		t.Errorf("the typed text is on standard error:\n%s", p.stderr)
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(30 * time.Second):
		t.Fatal("semar mcp did not end")
	}
}

func TestMCPStudioNeedsALoopbackAddress(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	var out, errb bytes.Buffer
	code := cli.RunWith(t.Context(), []string{"mcp", "--dir", r.dir, "--env", "local", "--studio=0.0.0.0:0"}, &out, &errb, r.opts)
	if code == cli.ExitOK || !strings.Contains(errb.String(), "must be on this machine") && !strings.Contains(errb.String(), "refused") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

// A person who holds control keeps the agent out before the journey's middleware runs: the baton is
// outside AutoLegMiddleware, so a refused launch neither opens a leg nor starts the recording of its flow.
func TestMCPStudioHumanInControlRefusesBeforeTheJourneyStartsALegFlow(t *testing.T) {
	r := journeyRig(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--studio=127.0.0.1:0")
	p.initialize()
	st := waitStudio(t, p)
	journeySay(t, p, "semar_journey_start", "id", "held", "title", "A journey while a person drives", "owner", "team-shop")
	journeySay(t, p, "semar_journey_add_leg", "id", "app", "surface", "android", "service", "shop-app")

	if code, _, body := st.post("/api/live/pause", map[string]string{"reason": "mine"}); code != 200 {
		t.Fatalf("pause = %d %s", code, body)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"mobile_launch_app", map[string]any{"packageName": androidPackage}},
		{"mobile_click_on_screen_at_coordinates", map[string]any{"x": 540, "y": 960}},
	} {
		res := p.callResult(tc.tool, tc.args)
		structured, _ := res["structuredContent"].(map[string]any)
		if res["isError"] != true || structured["status"] != "human_in_control" {
			t.Fatalf("%s while a person drives = %v", tc.tool, res)
		}
		if text := textOfResult(res); strings.Contains(text, "journey:") {
			t.Errorf("%s: the journey middleware ran before the baton refused: %s", tc.tool, text)
		}
	}
	if r.taps.Load() != 0 {
		t.Fatalf("%d taps reached the device", r.taps.Load())
	}

	// Had a leg flow been started by the refused calls, the launch after the hand-back would run
	// as a plain launch. It still is the first action of the leg: the recording starts then.
	if code, _, body := st.post("/api/live/resume", map[string]string{}); code != 200 {
		t.Fatalf("resume = %d %s", code, body)
	}
	if launch := journeySay(t, p, "mobile_launch_app", "packageName", androidPackage); !strings.Contains(launch, "started the app from a fresh start") {
		t.Errorf("the launch after the hand-back did not start the leg flow: %s", launch)
	}

	// Now that the android leg recording is running, pause again and call web_open: the baton must
	// refuse web_open before AutoLegMiddleware can end the android leg recording or open a web leg.
	if code, _, body := st.post("/api/live/pause", map[string]string{"reason": "keep recording"}); code != 200 {
		t.Fatalf("second pause = %d %s", code, body)
	}
	webRes := p.callResult("web_open", map[string]any{"service": "shop-web"})
	webStruct, _ := webRes["structuredContent"].(map[string]any)
	if webRes["isError"] != true || webStruct["status"] != "human_in_control" {
		t.Fatalf("web_open while a person drives = %v", webRes)
	}
	if text := textOfResult(webRes); strings.Contains(text, "journey:") {
		t.Errorf("web_open: journey middleware ran before the baton refused: %s", text)
	}
	status := journeySay(t, p, "semar_journey_status")
	if strings.Contains(status, "surface: web") {
		t.Errorf("web_open opened a web leg while human held control:\n%s", status)
	}
	if stepsRes := p.callResult("semar_record_steps", map[string]any{}); stepsRes["isError"] == true {
		t.Errorf("android leg recording was closed while human held control: %v", stepsRes)
	}
	if code, _, body := st.post("/api/live/resume", map[string]string{}); code != 200 {
		t.Fatalf("second resume = %d %s", code, body)
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

// While the agent drives, a burst of its calls costs the studio one reading of the screen, not one per
// call; a hand-back or an older cache reads again.
func TestMCPStudioPollsDoNotDumpTheScreenOncePerAgentCall(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	now := time.Unix(1_800_000_000, 0)
	var mu sync.Mutex
	opts := r.opts
	opts.Clock = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	p := startMCP(t, opts, "--dir", r.dir, "--env", "local", "--studio=127.0.0.1:0")
	p.initialize()
	st := waitStudio(t, p)
	if _, isErr := p.call("mobile_list_elements_on_screen", map[string]any{}); isErr {
		t.Fatalf("list\n%s", p.stderr)
	}
	st.state("/api/live/state") // the first reading
	dumps := func() int { return r.server.Count("GET /source") }
	base := dumps()
	for i := 0; i < 6; i++ {
		if res := p.callResult("mobile_click_on_screen_at_coordinates", map[string]any{"x": 1, "y": 1}); res["isError"] == true {
			t.Fatalf("click: %v", res)
		}
		st.state("/api/live/state")
	}
	burst := dumps()
	if burst-base > 1 {
		t.Errorf("6 agent calls and 6 polls cost %d dumps of the screen, want at most 1", burst-base)
	}
	// An older cache is read again.
	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	p.callResult("mobile_click_on_screen_at_coordinates", map[string]any{"x": 1, "y": 1})
	st.state("/api/live/state")
	if dumps() <= burst {
		t.Error("a cache older than half a second was not read again")
	}
}
