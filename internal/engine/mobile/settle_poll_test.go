package mobile_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

// A screen that moves for two reads and then stands still: the sleeps say how long a person waits.
func settleSleeps(t *testing.T, poll time.Duration) []time.Duration {
	t.Helper()
	d := mobiletest.NewFakeDriver()
	d.Show(mobiletest.Screen(mobiletest.Button("A", 10, 10)))
	now := time.Unix(0, 0)
	var sleeps []time.Duration
	sleep := func(_ context.Context, by time.Duration) error {
		sleeps = append(sleeps, by)
		now = now.Add(by)
		// The screen changes during the first two sleeps only.
		if len(sleeps) <= 2 {
			d.Show(mobiletest.Screen(mobiletest.Button("A", 10, 10+len(sleeps))))
		}
		return nil
	}
	if _, err := mobile.SettleEvery(context.Background(), d, 3*time.Second, poll, func() time.Time { return now }, sleep); err != nil {
		t.Fatal(err)
	}
	return sleeps
}

func TestSettleEveryKeepsAFixedDelayAfterTheFirst(t *testing.T) {
	ms := time.Millisecond
	if got, want := settleSleeps(t, 0), []time.Duration{150 * ms, 300 * ms, 500 * ms}; !slices.Equal(got, want) {
		t.Errorf("replay sleeps = %v, want %v", got, want)
	}
	if got, want := settleSleeps(t, 80*ms), []time.Duration{150 * ms, 80 * ms, 80 * ms}; !slices.Equal(got, want) {
		t.Errorf("recording sleeps = %v, want %v", got, want)
	}
}
