package domain

import (
	"fmt"
	"regexp"
	"time"
)

var runIDTime = regexp.MustCompile(`^r-([0-9]{8}T[0-9]{6}Z)-[0-9a-f]+$`)

// Time is the instant inside the ID, r-<UTC date and time>-<hex>, which is when the run began.
func (id RunID) Time() (time.Time, error) {
	m := runIDTime.FindStringSubmatch(string(id))
	if m == nil {
		return time.Time{}, fmt.Errorf("the run ID %q holds no time", string(id))
	}
	t, err := time.Parse("20060102T150405Z", m[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("the run ID %q holds no time", string(id))
	}
	return t, nil
}
