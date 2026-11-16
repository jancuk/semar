package fixture

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

// The base URLs of the rig end in /api, which the engine keeps in every URL it sends. The policy
// patterns name the whole path, so each rule must be checked against the URL that is sent.

func TestPrefixOnceBlocksBeforeAnyRequest(t *testing.T) {
	r := newRig(t, rigOptions{prefix: "/api", effects: config.Effects{Once: []config.OpPattern{{Method: "POST", Path: "/api/v1/payments"}}}})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: create[1]: POST /v1/payments matches effects.once, and a fixture runs on every run")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestPrefixOnceAtRunTime(t *testing.T) {
	r := newRig(t, rigOptions{prefix: "/api", effects: config.Effects{Once: []config.OpPattern{{Method: "POST", Path: "/api/v1/orders/o-1/cancel"}}}})
	res := r.resolve(request(orderGiven))
	if len(res.Blocked) > 0 {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	records := r.resolver().Release(t.Context(), res)
	want := []string{"order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) matches effects.once, and a fixture runs on every run"}
	if diff := diff(want, records[0].Left); diff != "" {
		t.Errorf("left (-want +got):\n%s", diff)
	}
	if r.app.Requests("POST", "/v1/orders/o-1/cancel") != 0 {
		t.Error("the cleanup that matches once was sent")
	}
}

func TestPrefixDenyBlocksBeforeAnyRequest(t *testing.T) {
	r := newRig(t, rigOptions{prefix: "/api", effects: config.Effects{Deny: []config.OpPattern{{Method: "POST", Path: "/api/v1/payments"}}}})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: create[1]: POST /v1/payments is on the deny list of uat")
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestPrefixFindMustRead(t *testing.T) {
	f := example(t)
	f.Find[0].Request.Method = "POST"
	f.Find[0].Request.Path = "/v1/orders/{{customer}}/search"
	f.Find[0].Request.Query = nil
	// The reads pattern names the path without the prefix, so the sent POST is a write.
	r := newRig(t, rigOptions{
		prefix:   "/api",
		fixtures: []domain.Fixture{f},
		effects:  config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/v1/orders/*/search"}}},
	})
	res := r.resolve(request(orderGiven))
	if len(res.Blocked) != 1 || !strings.Contains(res.Blocked[0], "a find step must read") {
		t.Fatalf("Blocked = %q", res.Blocked)
	}
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// A create that the policy classifies as a write made a record, even when the reads list names
// the same path without the prefix, so cleanup runs.
func TestPrefixWriteIsCleanedUp(t *testing.T) {
	r := newRig(t, rigOptions{
		prefix:       "/api",
		wrapPayments: failsPayments,
		effects:      config.Effects{Reads: []config.OpPattern{{Method: "POST", Path: "/v1/orders"}}},
	})
	res := r.resolve(request(orderGiven))
	wantBlocked(t, res, "fixture order.paid: find gave nothing and create step 2 (POST /v1/payments) answered 500")
	records := r.resolver().Release(t.Context(), res)
	if records[0].Cleanup != CleanupDone || r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Errorf("record = %+v, order %s", records[0], r.orderStatus("o-1"))
	}
}
