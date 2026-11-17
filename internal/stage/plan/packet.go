package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
)

// Packet is everything that one job needs to answer for one acceptance criterion. Code builds it
// from the repository without a model, so the job has nothing to look up. The same packet gives
// the same hash, and a job whose packet is unchanged since an earlier plan reuses that plan's answer.
type Packet struct {
	Ticket      string
	Number      int    // the number of the criterion in its ticket file
	Cite        string // <ticket file>:<line> of the criterion. Code writes it into the plan, never the model
	Criterion   string
	Operations  []PacketOperation
	Diff        string // the diff of the ticket's files, cut at MaxPacketDiffBytes
	DiffOmitted []string
	Fixtures    []fixture.Entry
	Accounts    []Account
	// Account is the account that a step acts as when it names none.
	Account string
	// Existing are the scenarios that the ticket has already, one line each. A job must not repeat
	// them, and they are not part of the hash: a plan that wrote them is the one that is reused.
	Existing []string
	// Version is the version of the prompt that the packet is put into. A new prompt gives new hashes.
	Version string
}

// Hash is a short digest of what the answer depends on: the ticket, the text of the criterion, the
// operations, the diff, the fixtures, the accounts, and the prompt version. The cite and the
// scenarios that exist are not in it, because a line can move and a plan adds scenarios without the
// answer to the criterion changing.
func (p Packet) Hash() string {
	view := struct {
		Ticket, Criterion, Diff, Account, Version string
		Operations                                []PacketOperation
		DiffOmitted                               []string
		Fixtures                                  []fixture.Entry
		Accounts                                  []Account
	}{p.Ticket, p.Criterion, p.Diff, p.Account, p.Version, p.Operations, p.DiffOmitted, p.Fixtures, p.Accounts}
	data, err := json.Marshal(view)
	if err != nil {
		panic(fmt.Sprintf("hash a packet: %v", err)) // the fields are strings, numbers, and slices of them
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// Packets builds one packet for each criterion of each ticket file of the change, in ticket and
// line order. A ticket with no ticket file or no criterion has none.
func Packets(in Input, version string) []Packet {
	var out []Packet
	for _, t := range in.Tickets {
		criteria := Criteria(t)
		if len(criteria) == 0 {
			continue
		}
		sections := ticketSections(in.Diff, in.Change.Files, t.Key)
		whole := joinSections(sections)
		diff, omitted := budgetDiff(sections, MaxPacketDiffBytes)
		regs := registrations(in.SourceDir, ticketFiles(in, t.Key))
		var existing []string
		for _, sc := range in.Existing[t.Key] {
			existing = append(existing, fmt.Sprintf("%s: %s", sc.ID, oneLine(sc.Title, maxProblemRunes)))
		}
		for _, c := range criteria {
			ops := ticketOperations(in.Knowledge, whole, t.Text, c.Full, regs)
			if len(in.Methods) > 0 {
				ops = slices.DeleteFunc(ops, func(op PacketOperation) bool { return !slices.Contains(in.Methods, op.Method) })
			}
			out = append(out, Packet{
				Ticket: t.Key, Number: c.Number, Cite: c.Cite(), Criterion: c.Full,
				Operations: ops, Diff: diff, DiffOmitted: omitted,
				Fixtures: in.Fixtures, Accounts: in.Accounts, Account: in.AuthProfile,
				Existing: existing, Version: version,
			})
		}
	}
	return out
}

// ticketFiles are the changed files that a ticket's commits touched, or every changed file when no
// file names the ticket.
func ticketFiles(in Input, key string) []domain.FileChange {
	var mine []domain.FileChange
	for _, f := range in.Change.Files {
		if slices.Contains(f.Tickets, key) {
			mine = append(mine, f)
		}
	}
	if len(mine) == 0 {
		return in.Change.Files
	}
	return mine
}

// packetTickets lists the keys of the tickets of the packets, sorted.
func packetTickets(ps []Packet) []string {
	set := map[string]bool{}
	for _, p := range ps {
		set[p.Ticket] = true
	}
	return slices.Sorted(maps.Keys(set))
}
