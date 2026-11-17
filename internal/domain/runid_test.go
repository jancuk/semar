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
