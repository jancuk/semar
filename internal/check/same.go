package check

import (
	"encoding/json"
	"regexp"
)

// jsonNumber is the number grammar of JSON. Text outside it is never read as a decimal, so
// "1/2", "0x10", "1_000", and "+5" stay text that must match character for character.
var jsonNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// Same compares raw captured values exactly. Two decimals are equal when their values are
// equal, whatever their text. Otherwise the texts must be equal. ok is false when the values
// cannot be compared: one holds a redaction marker, a decimal is too large to compare exactly,
// or fewer than two values were given.
func Same(values []string) (equal, ok bool) {
	if len(values) < 2 {
		return false, false
	}
	for _, v := range values {
		if hasMarker(v) {
			return false, false
		}
	}
	allDecimal := true
	for _, v := range values {
		if !jsonNumber.MatchString(v) {
			allDecimal = false
			break
		}
	}
	if !allDecimal {
		// A decimal and a word are never equal, and two words are equal only as text.
		for _, v := range values[1:] {
			if v != values[0] {
				return false, true
			}
		}
		return true, true
	}
	first, err := ratOf(json.Number(values[0]))
	if err != nil {
		return false, false
	}
	equal = true
	for _, v := range values[1:] {
		r, err := ratOf(json.Number(v))
		if err != nil {
			return false, false
		}
		if first.Cmp(r) != 0 {
			equal = false
		}
	}
	return equal, true
}
