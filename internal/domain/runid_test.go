package domain_test

import (
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

func TestRunIDTime(t *testing.T) {
	got, err := domain.RunID("r-20261118T071502Z-a1b2").Time()
	if err != nil || !got.Equal(time.Date(2026, 11, 18, 7, 15, 2, 0, time.UTC)) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "r1", "r-20261318T071502Z-a1b2", "r-20261118T071502-a1b2", "x-20261118T071502Z-a1b2", "r-20261118T071502Z"} {
		if _, err := domain.RunID(bad).Time(); err == nil {
			t.Errorf("%q must hold no time", bad)
		}
	}
}

func TestRunIDInFindsTheIDOfAnotherRunInAnyLetterCase(t *testing.T) {
	own := domain.RunID("r-20261118T004200Z-aaaa")
	for text, want := range map[string]string{
		"Board r-20261118T004030Z-96e2":        "r-20261118T004030Z-96e2",
		"#board-r-20261118t004030z-96e2 > li":  "r-20261118t004030z-96e2",
		"Board r-20261118T004200Z-aaaa":        "",
		"#board-r-20261118t004200z-aaaa":       "",
		"Order 2026-11-18, r-1, and no run ID": "",
	} {
		got, ok := domain.RunIDIn(text, own)
		if got != want || ok != (want != "") {
			t.Errorf("domain.RunIDIn(%q) = %q, %v, want %q", text, got, ok, want)
		}
	}
	if got, ok := domain.RunIDIn("Board r-20261118T004200Z-aaaa", ""); !ok || got == "" {
		t.Errorf("with no run of its own, every run ID is another run's: %q, %v", got, ok)
	}
}
