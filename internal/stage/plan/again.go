package plan

import "github.com/jancuk/semar/internal/domain"

// Earlier is one entry of an obligations file of a ticket, as far as a second plan needs it. The command
// reads it, and trusts nothing in it beyond what a scenario file confirms: Tests holds only scenarios
// whose files exist.
type Earlier struct {
	// Number and Key are the criterion that a plan after Plan 33 wrote the entry for. A plan before it
	// left them empty, and Cites links the entry instead.
	Number int
	Key    string
	// Cites are the cites of the criterion that an entry without a key holds: the ticket cites of its
	// scenarios, or its own cite when it is a manual check.
	Cites  []string
	Tests  []domain.ScenarioID
	Manual bool // the entry is a manual check of the criterion
}
