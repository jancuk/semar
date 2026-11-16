package recorder_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
)

var key = []byte("0123456789abcdef0123456789abcdef")

type fakeSink struct {
	mu         sync.Mutex
	actions    []domain.Action
	activities []domain.Activity
	writeErr   error
	appendErr  error
}

func (s *fakeSink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return "", s.writeErr
	}
	s.actions = append(s.actions, a)
	return "actions/" + string(a.ID) + ".json", nil
}

func (s *fakeSink) AppendActivity(e domain.Activity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendErr != nil {
		return s.appendErr
	}
	s.activities = append(s.activities, e)
	return nil
}

func classify(method string, _ *url.URL) effects.Class {
	if method == "GET" {
		return effects.Read
	}
	return effects.Write
}

func newRecorder(t *testing.T, sink recorder.Sink, secrets ...string) *recorder.Recorder {
	t.Helper()
	red, err := redact.New(config.Redact{}, func() []string { return secrets }, key)
	if err != nil {
		t.Fatal(err)
	}
	return recorder.New(sink, red, classify, "run-1")
}

func action(method string) domain.Action {
	return domain.Action{
		Request:  &domain.HTTPRequest{Method: method, URL: "https://t.example/v1/orders", PathTemplate: "/v1/orders"},
		Response: &domain.HTTPResponse{Status: 201},
	}
}

func TestRecordAssignsIdentity(t *testing.T) {
	sink := &fakeSink{}
	rec := newRecorder(t, sink)
	for want := 1; want <= 3; want++ {
		got, err := rec.Record(action("POST"))
		if err != nil {
			t.Fatal(err)
		}
		wantID := domain.ActionID(fmt.Sprintf("a-%04d", want))
		if got.Seq != want || got.ID != wantID || got.Schema != 1 || got.Run != "run-1" {
			t.Errorf("got seq %d id %q schema %d run %q", got.Seq, got.ID, got.Schema, got.Run)
		}
	}
	e := sink.activities[2]
	if e.Seq != 3 || e.Evidence != "actions/a-0003.json" || e.Verb != domain.VerbCreate {
		t.Errorf("activity = %+v", e)
	}
}

func TestRecordSinkFailure(t *testing.T) {
	boom := errors.New("disk full")
	cases := []struct {
		name string
		sink *fakeSink
	}{
		{"write fails", &fakeSink{writeErr: boom}},
		{"append fails", &fakeSink{appendErr: boom}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder(t, tc.sink)
			if _, err := rec.Record(action("POST")); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want it to wrap %v", err, boom)
			}
			tc.sink.writeErr, tc.sink.appendErr = nil, nil
			got, err := rec.Record(action("POST"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Seq != 2 {
				t.Errorf("seq after a failure = %d, want 2: the failed seq must not be reused", got.Seq)
			}
		})
	}
}

func TestRecordNeverKeepsASecret(t *testing.T) {
	const secret = "s3cr3t-token-value"
	sink := &fakeSink{}
	rec := newRecorder(t, sink, secret)
	a := action("POST")
	a.Response.Body = domain.Body(`{"token":"` + secret + `"}`)
	a.Err = "dial failed for " + secret
	got, err := rec.Record(a)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"stored": sink.actions[0], "returned": got, "activity": sink.activities[0]} {
		if s := fmt.Sprintf("%+v", v); strings.Contains(s, secret) {
			t.Errorf("%s holds the secret: %s", name, s)
		}
	}
	if string(a.Response.Body) != `{"token":"`+secret+`"}` {
		t.Error("the caller's action was changed")
	}
}

func TestRecorderIsSafeInParallel(t *testing.T) {
	const n = 50
	const secret = "parallel-secret"
	sink := &fakeSink{}
	rec := newRecorder(t, sink, secret)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := action("POST")
			a.Response.Body = domain.Body(secret)
			if _, err := rec.Record(a); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var seqs []int
	for _, a := range sink.actions {
		seqs = append(seqs, a.Seq)
		if strings.Contains(string(a.Response.Body), secret) {
			t.Errorf("stored action %s holds the secret", a.ID)
		}
	}
	slices.Sort(seqs)
	for i, s := range seqs {
		if s != i+1 {
			t.Fatalf("seqs are not 1..%d with no gap: %v", n, seqs)
		}
	}
	if len(seqs) != n || len(sink.activities) != n {
		t.Errorf("stored %d actions and %d activities, want %d each", len(seqs), len(sink.activities), n)
	}
}

// A value that a configured JSONPath removed from one action must not be stored by a later action
// of the run: a step or a cleanup that puts it in a path, a query, a body, or an error, nor the
// activity log that names the object of that path.
func TestJSONPathValueStaysMaskedForTheRun(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"string", `{"data":{"nik":"3201770011","name":"Ana"}}`},
		{"number", `{"data":{"nik":3201770011,"name":"Ana"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeSink{}
			red, err := redact.New(config.Redact{JSONPaths: []string{"$.data.nik"}}, func() []string { return nil }, key)
			if err != nil {
				t.Fatal(err)
			}
			rec := recorder.New(sink, red, classify, "run-1")
			marker := red.Marker("3201770011")
			first := domain.Action{
				Use:      domain.UseCase{Pass: domain.PassSetup},
				Request:  &domain.HTTPRequest{Method: "POST", URL: "https://h/v1/customers", PathTemplate: "/v1/customers"},
				Response: &domain.HTTPResponse{Status: 201, Body: []byte(tc.body)},
				Captures: map[string]string{"nik": "3201770011"},
			}
			got, err := rec.Record(first)
			if err != nil {
				t.Fatal(err)
			}
			if got.Captures["nik"] != marker {
				t.Fatalf("first capture = %q", got.Captures["nik"])
			}
			later := []domain.Action{
				{
					Use:      domain.UseCase{Pass: domain.PassScenario, Step: "read"},
					Request:  &domain.HTTPRequest{Method: "GET", URL: "https://h/v1/customers/3201770011?ref=3201770011", PathTemplate: "/v1/customers/{{c.nik}}"},
					Response: &domain.HTTPResponse{Status: 200, Body: []byte(`{"owner":"3201770011","seen":3201770011}`)},
					Captures: map[string]string{"owner": "3201770011"},
				},
				{
					Use:      domain.UseCase{Pass: domain.PassScenario, Step: "update"},
					Request:  &domain.HTTPRequest{Method: "PUT", URL: "https://h/v1/customers/3201770011", PathTemplate: "/v1/customers/{{c.nik}}", Body: []byte(`{"nik":"3201770011"}`)},
					Response: &domain.HTTPResponse{Status: 500},
					Err:      "customer 3201770011 failed",
				},
				{
					Use:      domain.UseCase{Pass: domain.PassCleanup},
					Request:  &domain.HTTPRequest{Method: "POST", URL: "https://h/v1/customers/3201770011/close", PathTemplate: "/v1/customers/{{c.nik}}/close"},
					Response: &domain.HTTPResponse{Status: 200},
				},
			}
			for _, a := range later {
				if _, err := rec.Record(a); err != nil {
					t.Fatal(err)
				}
			}
			for _, a := range sink.actions {
				b, _ := json.Marshal(a)
				if strings.Contains(string(b), "3201770011") {
					t.Errorf("action %s stores the value: %s", a.ID, b)
				}
			}
			for _, e := range sink.activities {
				b, _ := json.Marshal(e)
				if strings.Contains(string(b), "3201770011") {
					t.Errorf("activity %d stores the value: %s", e.Seq, b)
				}
			}
			if o := sink.activities[3].Object; o == nil || o.ID != marker {
				t.Errorf("the cleanup object = %+v, want the marker %s", o, marker)
			}
			if u := sink.actions[1].Request.URL; u != "https://h/v1/customers/"+marker+"?ref="+marker {
				t.Errorf("URL = %s", u)
			}
		})
	}
}
