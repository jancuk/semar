package fixture

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

func (r *rig) check() CheckResult {
	r.t.Helper()
	res, err := r.resolver().Check(r.t.Context(), "order.paid", "alice")
	if err != nil {
		r.t.Fatalf("Check: %v", err)
	}
	return res
}

func TestCheckHealthyWhenCreateMakesTheState(t *testing.T) {
	r := newRig(t, rigOptions{})
	res := r.check()
	rec := res.Record
	f := example(t)
	if !res.Checked || !rec.Healthy || rec.Reason != "" {
		t.Fatalf("result = %+v", res)
	}
	if rec.Fixture != "order.paid" || rec.Version != "1.0.0" || rec.Hash != domain.FixtureHash(f) || rec.Environment != testEnv || rec.Run != testRun {
		t.Errorf("record = %+v", rec)
	}
	if rec.Find != "nothing" || rec.Create != "made" || rec.Cleanup != "done" {
		t.Errorf("find %q, create %q, cleanup %q", rec.Find, rec.Create, rec.Cleanup)
	}
	if r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Error("the check did not cancel the order that create made")
	}
	for _, a := range r.sink.all() {
		if a.Use.Fixture == nil || a.Use.Step != "" || a.Use.Scenario != "" {
			t.Errorf("action %s has use %+v", a.ID, a.Use)
		}
	}
	if len(res.Actions) != 6 {
		t.Errorf("%d actions, want 6", len(res.Actions))
	}
}

func TestCheckRunsCreateAlsoWhenFindFoundARecord(t *testing.T) {
	r := newRig(t, rigOptions{})
	seeded := r.seedPaidOrder("c-1")
	res := r.check()
	if !res.Checked || !res.Record.Healthy || res.Record.Find != "found" || res.Record.Create != "made" || res.Record.Cleanup != "done" {
		t.Fatalf("result = %+v", res)
	}
	if got := r.orderStatus(seeded); got != sampleapp.StatusPaid {
		t.Errorf("the record that find reused is %s, want it untouched", got)
	}
	if got := r.orderStatus("o-2"); got != sampleapp.StatusCancelled {
		t.Errorf("the record that create made is %s", got)
	}
}

func TestCheckUnhealthyWhenCreateFails(t *testing.T) {
	r := newRig(t, rigOptions{defects: []sampleapp.Defect{sampleapp.PaymentNeverSettles}})
	res := r.check()
	rec := res.Record
	if !res.Checked || rec.Healthy || rec.Create != "failed" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(rec.Reason, "create step 3 (GET /v1/orders/{{id}}) waited 30s") {
		t.Errorf("reason = %q", rec.Reason)
	}
	if rec.Cleanup != "done" || r.orderStatus("o-1") != sampleapp.StatusCancelled {
		t.Errorf("cleanup %q, order %s: what create made must still be removed", rec.Cleanup, r.orderStatus("o-1"))
	}
}

func TestCheckCleanupFailureIsHealthyAndListed(t *testing.T) {
	r := newRig(t, rigOptions{defects: []sampleapp.Defect{sampleapp.CancelFails}})
	res := r.check()
	if !res.Checked || !res.Record.Healthy || res.Record.Cleanup != "failed" || len(res.Left) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Left[0], "order created by fixture order.paid") || !strings.Contains(res.Left[0], "answered 500") {
		t.Errorf("left = %q", res.Left)
	}
}

func TestCheckIgnoresItsOwnLastHealthRecord(t *testing.T) {
	r := newRig(t, rigOptions{})
	f := example(t)
	bad := HealthRecord{Fixture: f.ID, Version: f.Version, Hash: domain.FixtureHash(f), Environment: testEnv,
		At: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC), Find: "nothing", Create: "failed", Cleanup: "not_needed", Reason: "it broke"}
	if err := r.health.Put(bad); err != nil {
		t.Fatal(err)
	}
	if res := r.check(); !res.Checked || !res.Record.Healthy {
		t.Fatalf("a failed record kept the fixture from being checked again: %+v", res)
	}
}

func TestCheckNeedsApprovalAndSendsNothingWithout(t *testing.T) {
	r := newRig(t, rigOptions{unapproved: true})
	res := r.check()
	if res.Checked || !strings.Contains(res.Note, "fixture order.paid is not approved: run semar confirm --fixture order.paid") {
		t.Fatalf("result = %+v", res)
	}
	if n := r.sent("", ""); n != 0 {
		t.Errorf("%d requests were sent for a fixture nobody approved", n)
	}
	missing, err := r.resolver().Check(t.Context(), "order.refunded", "alice")
	if err != nil || missing.Checked || !strings.Contains(missing.Note, "fixture order.refunded (as check) is missing") {
		t.Errorf("a fixture that is not in the catalogue: %+v, %v", missing, err)
	}
}

func TestCheckOnAReadOnlyEnvironmentSendsNoWrite(t *testing.T) {
	r := newRig(t, rigOptions{effects: config.Effects{Mode: "read_only"}})
	res := r.check()
	if res.Checked || res.Record.Create != "not_tried" || !strings.Contains(res.Note, "this environment is read-only") {
		t.Fatalf("result = %+v", res)
	}
	if n := r.sent("POST", ""); n != 0 {
		t.Errorf("%d writes were sent to a read-only environment", n)
	}
}

func TestCheckOfAFixtureThatOnlyFinds(t *testing.T) {
	f := example(t)
	f.Create, f.Cleanup = nil, nil
	r := newRig(t, rigOptions{fixtures: []domain.Fixture{f}, effects: config.Effects{Mode: "read_only"}})
	empty := r.check()
	if !empty.Checked || empty.Record.Healthy || empty.Record.Find != "nothing" || empty.Record.Create != "none" {
		t.Fatalf("nothing to find: %+v", empty)
	}
	if !strings.Contains(empty.Record.Reason, "find gave nothing and the fixture has no create steps") {
		t.Errorf("reason = %q", empty.Record.Reason)
	}
	r.seedPaidOrder("c-1")
	found := r.check()
	if !found.Checked || !found.Record.Healthy || found.Record.Find != "found" || found.Record.Cleanup != "not_needed" {
		t.Errorf("a record to find: %+v", found)
	}
}

func TestCheckTakesNoLease(t *testing.T) {
	f := example(t)
	f.Lease = domain.LeaseExclusive
	r := newRig(t, rigOptions{fixtures: []domain.Fixture{f}, noLeases: true})
	if res := r.check(); !res.Checked || !res.Record.Healthy {
		t.Fatalf("a check of an exclusive fixture needs no lease directory: %+v", res)
	}
}
