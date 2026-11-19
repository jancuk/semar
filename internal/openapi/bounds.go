package openapi

import "strconv"

// Limits are the bounds that the document gives a value. A bound that the document does not give is nil.
type Limits struct {
	MinLength, MaxLength *int
	Minimum, Maximum     *float64
}

func limitsOf(schema map[string]any) Limits {
	var l Limits
	if n, err := strconv.Atoi(str(schema["minLength"])); err == nil && n >= 0 {
		l.MinLength = &n
	}
	if n, err := strconv.Atoi(str(schema["maxLength"])); err == nil && n >= 0 {
		l.MaxLength = &n
	}
	if f, err := strconv.ParseFloat(str(schema["minimum"]), 64); err == nil {
		l.Minimum = &f
	}
	if f, err := strconv.ParseFloat(str(schema["maximum"]), 64); err == nil {
		l.Maximum = &f
	}
	return l
}
