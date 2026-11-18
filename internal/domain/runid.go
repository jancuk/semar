package domain

import (
	"fmt"
	"regexp"
	"strings"
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

var runIDInText = regexp.MustCompile(`(?i)r-[0-9]{8}t[0-9]{6}z-[0-9a-f]{4,}`)

// RunIDIn finds the first run ID in a text that is not own. The letter case is not read, because a
// page puts the name of a record in lower case in an address or an id.
func RunIDIn(text string, own RunID) (string, bool) {
	for _, m := range runIDInText.FindAllString(text, -1) {
		if !strings.EqualFold(m, string(own)) {
			return m, true
		}
	}
	return "", false
}
