package activity

import (
	"strconv"

	"github.com/jancuk/semar/internal/domain"
)

type LedgerRow struct {
	Verb      domain.Verb
	Object    domain.ObjectRef
	Use       domain.UseCase
	CleanedUp bool
}

// Ledger lists what the run changed in the test environment, in the order of
// first appearance. A delete is not a row: it marks the creates it undid.
func Ledger(entries []domain.Activity) []LedgerRow {
	var rows []LedgerRow
	for _, e := range entries {
		if e.Object == nil || !succeeded(e.Result) {
			continue
		}
		switch e.Verb {
		case domain.VerbCreate, domain.VerbUpdate:
			rows = append(rows, LedgerRow{Verb: e.Verb, Object: *e.Object, Use: e.Use})
		case domain.VerbDelete:
			if e.Object.ID == "" {
				continue
			}
			for i := range rows {
				if rows[i].Verb == domain.VerbCreate && rows[i].Object == *e.Object {
					rows[i].CleanedUp = true
				}
			}
		}
	}
	return rows
}

func succeeded(result string) bool {
	status, err := strconv.Atoi(result)
	return err == nil && status < 400
}
