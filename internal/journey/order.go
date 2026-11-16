package journey

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// Order is a topological order of needs: a leg comes after every leg it needs. Among the legs
// that are ready, the one that comes first in the file goes first. A leg that needs itself, a
// leg that is not in the file, and a cycle are errors.
func Order(j domain.Journey) ([]domain.LegID, error) {
	index := make(map[domain.LegID]int, len(j.Legs))
	for i, l := range j.Legs {
		if _, dup := index[l.ID]; dup {
			return nil, fmt.Errorf("leg %s is used twice", l.ID)
		}
		index[l.ID] = i
	}
	waiting := make([]int, len(j.Legs))     // needs not yet placed
	followers := make([][]int, len(j.Legs)) // legs that need this one
	for i, l := range j.Legs {
		seen := map[domain.LegID]bool{}
		for _, n := range l.Needs {
			k, ok := index[n]
			switch {
			case !ok:
				return nil, fmt.Errorf("leg %s needs %s, which is not a leg of the journey", l.ID, n)
			case seen[n]:
				continue
			}
			seen[n] = true
			waiting[i]++
			followers[k] = append(followers[k], i)
		}
	}

	var ready []int // kept sorted, so ready[0] is the first in the file
	for i, w := range waiting {
		if w == 0 {
			ready = append(ready, i)
		}
	}
	out := make([]domain.LegID, 0, len(j.Legs))
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		out = append(out, j.Legs[i].ID)
		for _, f := range followers[i] {
			if waiting[f]--; waiting[f] == 0 {
				at, _ := slices.BinarySearch(ready, f)
				ready = slices.Insert(ready, at, f)
			}
		}
	}
	if len(out) < len(j.Legs) {
		return nil, fmt.Errorf("needs has a cycle: %s", joinLegs(findCycle(j, index, false)))
	}
	return out, nil
}

// findCycle returns the legs of the first cycle that a walk in file order finds, with the first leg
// repeated at the end, such as pay, settled, pay. A leg that needs itself is a cycle of one, which
// skipSelf leaves out.
func findCycle(j domain.Journey, index map[domain.LegID]int, skipSelf bool) []domain.LegID {
	const (
		fresh = iota
		onPath
		done
	)
	state := make([]int, len(j.Legs))
	var path []int
	var cycle []domain.LegID
	var walk func(i int) bool
	walk = func(i int) bool {
		state[i] = onPath
		path = append(path, i)
		for _, n := range j.Legs[i].Needs {
			k, ok := index[n]
			if !ok || skipSelf && k == i {
				continue
			}
			switch state[k] {
			case onPath:
				for _, p := range path[slices.Index(path, k):] {
					cycle = append(cycle, j.Legs[p].ID)
				}
				cycle = append(cycle, j.Legs[k].ID)
				return true
			case fresh:
				if walk(k) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		state[i] = done
		return false
	}
	for i := range j.Legs {
		if state[i] == fresh && walk(i) {
			return cycle
		}
	}
	return nil
}

func joinLegs(ids []domain.LegID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = string(id)
	}
	return strings.Join(s, ", ")
}
