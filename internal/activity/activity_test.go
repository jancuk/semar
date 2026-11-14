package activity_test

import (
	"bytes"
	"flag"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestFromAction(t *testing.T) {
	req := func(method, template, rawURL string) *domain.HTTPRequest {
		return &domain.HTTPRequest{Method: method, PathTemplate: template, URL: rawURL}
	}
	resp := func(status int) *domain.HTTPResponse { return &domain.HTTPResponse{Status: status} }
	cases := []struct {
		name       string
		action     domain.Action
		class      effects.Class
		wantVerb   domain.Verb
		wantTarget string
		wantObject *domain.ObjectRef
		wantResult string
	}{
		{"create with capture", domain.Action{Request: req("POST", "/v1/orders", "https://t.example/v1/orders?x=1"), Response: resp(201), Captures: map[string]string{"order_id": "81234"}},
			effects.Write, "create", "POST /v1/orders", &domain.ObjectRef{Type: "order", ID: "81234"}, "201"},
		{"first capture in name order", domain.Action{Request: req("POST", "/v1/orders", "https://t.example/v1/orders"), Response: resp(201), Captures: map[string]string{"b": "2", "a": "1"}},
			effects.Write, "create", "POST /v1/orders", &domain.ObjectRef{Type: "order", ID: "1"}, "201"},
		{"read by id", domain.Action{Request: req("GET", "/v1/orders/{id}", "https://t.example/v1/orders/81234"), Response: resp(200)},
			effects.Read, "read", "GET /v1/orders/81234", &domain.ObjectRef{Type: "order", ID: "81234"}, "200"},
		{"nested create without capture", domain.Action{Request: req("POST", "/v1/orders/{id}/refunds", "https://t.example/v1/orders/81234/refunds"), Response: resp(422)},
			effects.Write, "create", "POST /v1/orders/81234/refunds", &domain.ObjectRef{Type: "refund"}, "422"},
		{"update", domain.Action{Request: req("PATCH", "/v1/orders/{id}", "https://t.example/v1/orders/81234"), Response: resp(200)},
			effects.Write, "update", "PATCH /v1/orders/81234", &domain.ObjectRef{Type: "order", ID: "81234"}, "200"},
		{"put is update", domain.Action{Request: req("PUT", "/v1/orders/{id}", "https://t.example/v1/orders/9"), Response: resp(200)},
			effects.Write, "update", "PUT /v1/orders/9", &domain.ObjectRef{Type: "order", ID: "9"}, "200"},
		{"delete", domain.Action{Request: req("DELETE", "/v1/addresses/{id}", "https://t.example/v1/addresses/5512"), Response: resp(204)},
			effects.Write, "delete", "DELETE /v1/addresses/5512", &domain.ObjectRef{Type: "address", ID: "5512"}, "204"},
		{"search read has no object", domain.Action{Request: req("POST", "/v1/search", "https://t.example/v1/search"), Response: resp(200)},
			effects.Read, "read", "POST /v1/search", nil, "200"},
		{"list read has no object", domain.Action{Request: req("GET", "/v1/orders", "https://t.example/v1/orders"), Response: resp(200)},
			effects.Read, "read", "GET /v1/orders", nil, "200"},
		{"refused", domain.Action{Request: req("DELETE", "/v1/orders/{id}", "https://t.example/v1/orders/1"), Refused: "denied: matches the deny list"},
			effects.Write, "delete", "DELETE /v1/orders/1", nil, "refused: denied: matches the deny list"},
		{"transport error", domain.Action{Request: req("POST", "/v1/orders", "https://t.example/v1/orders"), Err: "connection reset", Captures: map[string]string{"a": "1"}},
			effects.Write, "create", "POST /v1/orders", nil, "error: connection reset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.action.Seq = 7
			got := activity.FromAction(tc.action, tc.class, "actions/a-0007.json")
			if got.Verb != tc.wantVerb || got.Target != tc.wantTarget || got.Result != tc.wantResult {
				t.Errorf("got verb %q target %q result %q, want %q %q %q", got.Verb, got.Target, got.Result, tc.wantVerb, tc.wantTarget, tc.wantResult)
			}
			if !reflect.DeepEqual(got.Object, tc.wantObject) {
				t.Errorf("object = %+v, want %+v", got.Object, tc.wantObject)
			}
			if got.Seq != 7 || got.Evidence != "actions/a-0007.json" {
				t.Errorf("seq %d evidence %q not carried over", got.Seq, got.Evidence)
			}
		})
	}
}

func entry(verb domain.Verb, typ, id, result string) domain.Activity {
	return domain.Activity{Verb: verb, Object: &domain.ObjectRef{Type: typ, ID: id}, Result: result}
}

func TestLedger(t *testing.T) {
	cases := []struct {
		name    string
		entries []domain.Activity
		want    []activity.LedgerRow
	}{
		{"create update delete read", []domain.Activity{
			entry(domain.VerbCreate, "order", "1", "201"),
			entry(domain.VerbCreate, "order", "2", "201"),
			entry(domain.VerbUpdate, "order", "2", "200"),
			entry(domain.VerbDelete, "order", "1", "204"),
			entry(domain.VerbRead, "order", "2", "200"),
		}, []activity.LedgerRow{
			{Verb: domain.VerbCreate, Object: domain.ObjectRef{Type: "order", ID: "1"}, CleanedUp: true},
			{Verb: domain.VerbCreate, Object: domain.ObjectRef{Type: "order", ID: "2"}},
			{Verb: domain.VerbUpdate, Object: domain.ObjectRef{Type: "order", ID: "2"}},
		}},
		{"failed create is absent", []domain.Activity{
			entry(domain.VerbCreate, "order", "1", "400"),
			entry(domain.VerbCreate, "order", "2", "refused: denied"),
		}, nil},
		{"failed delete does not clean up", []domain.Activity{
			entry(domain.VerbCreate, "order", "1", "201"),
			entry(domain.VerbDelete, "order", "1", "500"),
		}, []activity.LedgerRow{{Verb: domain.VerbCreate, Object: domain.ObjectRef{Type: "order", ID: "1"}}}},
		{"delete of another type does not clean up", []domain.Activity{
			entry(domain.VerbCreate, "order", "1", "201"),
			entry(domain.VerbDelete, "address", "1", "204"),
		}, []activity.LedgerRow{{Verb: domain.VerbCreate, Object: domain.ObjectRef{Type: "order", ID: "1"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := activity.Ledger(tc.entries); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func timelineEntries() []domain.Activity {
	at := time.Date(2026, 11, 14, 10, 6, 5, 0, time.UTC)
	mk := func(seq int, pass domain.Pass, sc domain.ScenarioID, verb domain.Verb, target, result, why string) domain.Activity {
		return domain.Activity{Seq: seq, At: at.Add(time.Duration(seq) * time.Second), Use: domain.UseCase{Pass: pass, Scenario: sc}, Verb: verb, Target: target, Result: result, Why: why}
	}
	return []domain.Activity{
		mk(1, domain.PassSetup, "", domain.VerbCreate, "POST /v1/users", "201", "seed a user"),
		mk(2, domain.PassScenario, "S-1", domain.VerbRead, "GET /v1/orders", "200", ""),
		mk(3, domain.PassScenario, "S-1", domain.VerbCreate, "POST /v1/orders", "201", "place an order"),
		mk(4, domain.PassScenario, "S-2", domain.VerbUpdate, "PATCH /v1/orders/9", "422", ""),
		mk(5, domain.PassExplore, "S-2", domain.VerbRead, "GET /v1/orders/9", "refused: denied", ""),
		mk(6, domain.PassCleanup, "", domain.VerbDelete, "DELETE /v1/users/3", "204", "remove the user"),
	}
}

func TestTimeline(t *testing.T) {
	var buf bytes.Buffer
	if err := activity.Timeline(&buf, timelineEntries(), activity.Filter{}); err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/timeline.golden"
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Errorf("timeline differs from %s\ngot:\n%s\nwant:\n%s", golden, buf.String(), want)
	}
}

func TestTimelineFilter(t *testing.T) {
	cases := []struct {
		name   string
		filter activity.Filter
		want   []int
	}{
		{"writes only", activity.Filter{WritesOnly: true}, []int{1, 3, 4, 6}},
		{"scenario", activity.Filter{Scenario: "S-1"}, []int{2, 3}},
		{"pass", activity.Filter{Pass: domain.PassExplore}, []int{5}},
		{"combined", activity.Filter{WritesOnly: true, Scenario: "S-2"}, []int{4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := activity.Timeline(&buf, timelineEntries(), tc.filter); err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
			if len(lines)-1 != len(tc.want) {
				t.Fatalf("got %d rows, want %d:\n%s", len(lines)-1, len(tc.want), buf.String())
			}
			for i, seq := range tc.want {
				if f := bytes.Fields(lines[i+1]); string(f[0]) != string(rune('0'+seq)) {
					t.Errorf("row %d starts with %s, want %d", i, f[0], seq)
				}
			}
		})
	}
}
