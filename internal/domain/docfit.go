package domain

import (
	"fmt"
	"strings"
)

// Misfit is one difference between a response body and its schema in the API document. It holds
// a path and types, and never a value of the response.
type Misfit struct {
	Path string `json:"path"` // a JSON path such as $.data.items[0].id
	Want string `json:"want"` // what the document says, such as "integer"
	Got  string `json:"got"`  // the JSON type that came, or "missing"
}

// DocCheck is what the check of a response against the API document found. An action holds one
// only when there is something to say, so a run from before this check has none.
type DocCheck struct {
	Env       string `json:"env,omitempty"`
	Operation string `json:"operation"` // as the document writes it, such as GET /orders/{id}
	// Misfits are at most MaxMisfits, in path order. MoreMisfits counts the rest.
	Misfits     []Misfit `json:"misfits,omitempty"`
	MoreMisfits int      `json:"moreMisfits,omitempty"`
	// SchemaFail is true when environments.<name>.checks.schema was fail while the response was checked.
	SchemaFail bool `json:"schemaFail,omitempty"`
	// Undeclared is true when the operation does not declare the status that came, and Status is that status.
	Undeclared bool `json:"undeclared,omitempty"`
	Status     int  `json:"status,omitempty"`
	// LimitMS and TookMS are set when the response took longer than the time limit of the environment.
	LimitMS  int64 `json:"limitMs,omitempty"`
	TookMS   int64 `json:"tookMs,omitempty"`
	SlowFail bool  `json:"slowFail,omitempty"`
}

// MaxMisfits is how many misfits one step keeps.
const MaxMisfits = 5

// Slow reports whether the response was over the time limit.
func (d DocCheck) Slow() bool { return d.LimitMS > 0 }

// Text says the misfit in one sentence, with the path as a person reads it.
func (m Misfit) Text() string {
	p := strings.TrimPrefix(m.Path, "$.")
	if p == "$" {
		p = "the body"
	}
	if m.Got == "missing" {
		return fmt.Sprintf("%s is missing, and the document says it is %s", p, m.Want)
	}
	article := "a "
	if strings.ContainsAny(m.Got[:min(1, len(m.Got))], "aeiou") {
		article = "an "
	}
	return fmt.Sprintf("%s is %s%s, and the document says %s", p, article, m.Got, m.Want)
}

// SchemaText is the sentence for the misfits of a check, the first one in full and a count of the rest.
func (d DocCheck) SchemaText() string {
	if len(d.Misfits) == 0 {
		return ""
	}
	text := d.Misfits[0].Text()
	if rest := len(d.Misfits) - 1 + d.MoreMisfits; rest > 0 {
		text += fmt.Sprintf(" (and %d more)", rest)
	}
	return text
}
