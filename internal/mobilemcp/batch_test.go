package mobilemcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/mcp"
)

func TestBatchRunsStepsInOrder(t *testing.T) {
	f := newFixture(t)
	res := f.ok("mobile_batch_commands", `{"steps":[
		{"name":"mobile_click_on_screen_at_coordinates","arguments":{"x":10,"y":20}},
		{"name":"delay","arguments":{"milliseconds":1}},
		{"name":"mobile_press_button","arguments":{"button":"BACK"}},
		{"name":"mobile_get_screen_size"}
	]}`)
	var texts []string
	for _, c := range res.Content {
		texts = append(texts, c.Text)
	}
	if len(texts) != 4 {
		t.Fatalf("texts = %q", texts)
	}
	for i, want := range []string{"step 1 mobile_click_on_screen_at_coordinates: tapped (10,20)", "step 2 delay: waited 1 ms", "step 3 mobile_press_button: pressed BACK", "step 4 mobile_get_screen_size: {"} {
		if !strings.HasPrefix(texts[i], want) {
			t.Errorf("text %d = %q, want %q", i, texts[i], want)
		}
	}
	if len(f.dev.touches) != 1 || len(f.dev.keys) != 1 || f.dev.keys[0] != 4 {
		t.Errorf("calls = %v", f.dev.calls)
	}
}

func TestBatchStopsOnErrorByDefault(t *testing.T) {
	f := newFixture(t)
	res := f.call("mobile_batch_commands", `{"steps":[
		{"name":"mobile_press_button","arguments":{"button":"HOME"}},
		{"name":"mobile_press_button","arguments":{"button":"WARP"}},
		{"name":"mobile_press_button","arguments":{"button":"BACK"}}
	]}`)
	text := textOf(res)
	if !res.IsError || !strings.Contains(text, "step 2 mobile_press_button failed") || !strings.Contains(text, "stopped: 1 steps were not run") {
		t.Errorf("result = %s", text)
	}
	if len(f.dev.keys) != 1 {
		t.Errorf("keys = %v", f.dev.keys)
	}
}

func TestBatchContinuesWhenStopOnErrorIsFalse(t *testing.T) {
	f := newFixture(t)
	res := f.call("mobile_batch_commands", `{"stopOnError":false,"steps":[
		{"name":"mobile_press_button","arguments":{"button":"WARP"}},
		{"name":"mobile_press_button","arguments":{"button":"BACK"}},
		{"name":"nope"}
	]}`)
	text := textOf(res)
	if !res.IsError || !strings.Contains(text, "step 1") || !strings.Contains(text, "step 2 mobile_press_button: pressed BACK") ||
		!strings.Contains(text, `step 3 nope failed: the tool "nope" is not a mobile tool`) || strings.Contains(text, "stopped") {
		t.Errorf("result = %s", text)
	}
	if len(f.dev.keys) != 1 {
		t.Errorf("keys = %v", f.dev.keys)
	}
}

func TestBatchDelayBounds(t *testing.T) {
	f := newFixture(t)
	for _, ms := range []string{"0", "-1", "10001", "999999"} {
		res := f.call("mobile_batch_commands", `{"steps":[{"name":"delay","arguments":{"milliseconds":`+ms+`}}]}`)
		if !res.IsError || !strings.Contains(textOf(res), "1 to 10000") {
			t.Errorf("%s: %s", ms, textOf(res))
		}
	}
	res := f.call("mobile_batch_commands", `{"steps":[{"name":"delay"}]}`)
	if !res.IsError {
		t.Errorf("a delay with no time: %s", textOf(res))
	}
	f.ok("mobile_batch_commands", `{"steps":[{"name":"delay","arguments":{"milliseconds":1}}]}`)
}

func TestBatchRejectsNestingAndSemarTools(t *testing.T) {
	f := newFixture(t)
	f.fails("mobile_batch_commands", `{"steps":[{"name":"mobile_batch_commands","arguments":{"steps":[{"name":"delay","arguments":{"milliseconds":1}}]}}]}`, "cannot hold a batch")
	f.fails("mobile_batch_commands", `{"steps":[{"name":"delay","arguments":{"milliseconds":1}},{"name":"semar_record_start"}]}`, "mobile tools only")
	if len(f.dev.calls) != 0 {
		t.Errorf("a refused batch ran: %v", f.dev.calls)
	}
}

func TestBatchStepCount(t *testing.T) {
	f := newFixture(t)
	steps := func(n int) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, `{"name":"delay","arguments":{"milliseconds":1}}`)
		}
		return `{"steps":[` + strings.Join(parts, ",") + `]}`
	}
	f.fails("mobile_batch_commands", `{"steps":[]}`, "1 to 50")
	f.fails("mobile_batch_commands", steps(51), "1 to 50")
	res := f.ok("mobile_batch_commands", steps(50))
	if len(res.Content) != 50 {
		t.Errorf("%d texts", len(res.Content))
	}
}

func TestBatchImagesFollowTheTextInStepOrder(t *testing.T) {
	f := newFixture(t)
	res := f.ok("mobile_batch_commands", `{"steps":[
		{"name":"mobile_take_screenshot","arguments":{"clip":{"x":0,"y":0,"width":100,"height":100}}},
		{"name":"mobile_press_button","arguments":{"button":"HOME"}},
		{"name":"mobile_take_screenshot","arguments":{"clip":{"x":0,"y":0,"width":200,"height":300}}}
	]}`)
	var kinds []string
	for _, c := range res.Content {
		kinds = append(kinds, c.Type)
	}
	if strings.Join(kinds, ",") != "text,text,text,image,image" {
		t.Fatalf("kinds = %v", kinds)
	}
	if !strings.Contains(res.Content[0].Text, "image 100x100") || !strings.Contains(res.Content[0].Text, "[image 1 below]") ||
		!strings.Contains(res.Content[2].Text, "image 200x300") || !strings.Contains(res.Content[2].Text, "[image 2 below]") {
		t.Errorf("texts = %q / %q", res.Content[0].Text, res.Content[2].Text)
	}
	w1, h1, _ := imageOf(t, mcp.Result{Content: res.Content[3:4]})
	w2, h2, _ := imageOf(t, mcp.Result{Content: res.Content[4:5]})
	if w1 != 100 || h1 != 100 || w2 != 200 || h2 != 300 {
		t.Errorf("images %dx%d, %dx%d", w1, h1, w2, h2)
	}
}

func TestBatchListElementsAtEndAndSharedRefs(t *testing.T) {
	f := newFixture(t)
	res := f.ok("mobile_batch_commands", `{"listElementsAtEnd":true,"steps":[
		{"name":"mobile_list_elements_on_screen"},
		{"name":"mobile_click_on_screen_at_coordinates","arguments":{"ref":"e3"}}
	]}`)
	last := res.Content[len(res.Content)-1].Text
	if !strings.HasPrefix(last, "elements on the screen:") || !strings.Contains(last, "window app com.shop") {
		t.Errorf("last = %q", last)
	}
	if g := lastTouch(t, f); g.From.X != 540 || g.From.Y != 1620 {
		t.Errorf("gesture = %+v", g)
	}
	// With a batch that failed, the listing still comes.
	res = f.call("mobile_batch_commands", `{"listElementsAtEnd":true,"steps":[{"name":"mobile_press_button","arguments":{"button":"WARP"}}]}`)
	if !res.IsError || !strings.Contains(textOf(res), "elements on the screen:") {
		t.Errorf("result = %s", textOf(res))
	}
}

func TestBatchReportsProgress(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var seen []string
	call := mcp.Call{Name: "mobile_batch_commands", Args: json.RawMessage(`{"steps":[{"name":"delay","arguments":{"milliseconds":1}},{"name":"mobile_get_orientation"}]}`),
		Progress: func(done, total float64, msg string) {
			mu.Lock()
			seen = append(seen, fmt.Sprintf("%v/%v %s", done, total, msg))
			mu.Unlock()
		}}
	if _, err := f.tools["mobile_batch_commands"].Handler(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ";") != "0/2 delay;1/2 mobile_get_orientation" {
		t.Errorf("progress = %v", seen)
	}
}

func TestBatchStopsWhenTheCallIsCancelled(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call := mcp.Call{Name: "mobile_batch_commands", Args: json.RawMessage(`{"steps":[{"name":"mobile_get_orientation"}]}`)}
	res, err := f.tools["mobile_batch_commands"].Handler(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || f.dev.called("Orientation") != 0 {
		t.Errorf("result = %s, calls = %v", textOf(res), f.dev.calls)
	}
}

func TestBatchStepsGoThroughTheGuardAndTheScrub(t *testing.T) {
	f := newFixture(t)
	f.scrub.Add("tok_live_9f8e7d6c")
	f.dev.clip = "the token is tok_live_9f8e7d6c"
	res := f.ok("mobile_batch_commands", `{"steps":[{"name":"mobile_clipboard"}]}`)
	if strings.Contains(textOf(res), "tok_live_9f8e7d6c") {
		t.Errorf("a secret came out of a batch: %s", textOf(res))
	}
	f.dev.err["OpenURL"] = fmt.Errorf("could not open for tok_live_9f8e7d6c")
	res = f.call("mobile_batch_commands", `{"steps":[{"name":"mobile_open_url","arguments":{"url":"https://example.com"}}]}`)
	if !res.IsError || strings.Contains(textOf(res), "tok_live_9f8e7d6c") || !strings.Contains(textOf(res), "[redacted:") {
		t.Errorf("a secret came out of a step error: %s", textOf(res))
	}
}
