package journey

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func legs(spec ...string) domain.Journey {
	var j domain.Journey
	for _, s := range spec {
		id, needs, _ := strings.Cut(s, ":")
		l := domain.Leg{ID: domain.LegID(id)}
		for _, n := range strings.Fields(needs) {
			l.Needs = append(l.Needs, domain.LegID(n))
		}
		j.Legs = append(j.Legs, l)
	}
	return j
}

func TestOrderOfTheExample(t *testing.T) {
	got, err := Order(example(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.LegID{"place", "pay", "settled"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestOrderTopologicalWithFileTies(t *testing.T) {
	for _, tc := range []struct {
		name string
		legs []string
		want []domain.LegID
	}{
		{"no needs keeps the file order", []string{"c", "a", "b"}, []domain.LegID{"c", "a", "b"}},
		{"a leg waits for its need", []string{"a:c", "b", "c"}, []domain.LegID{"b", "c", "a"}},
		{"a freed leg goes by file order", []string{"d", "c:a", "b:a", "a"}, []domain.LegID{"d", "a", "c", "b"}},
		{"an earlier leg is freed late", []string{"a:z", "b", "z:b"}, []domain.LegID{"b", "z", "a"}},
		{"a diamond", []string{"top", "left:top", "right:top", "join:left right"}, []domain.LegID{"top", "left", "right", "join"}},
		{"a need listed twice", []string{"a:b b", "b"}, []domain.LegID{"b", "a"}},
		{"later legs with no need come after earlier ready legs", []string{"a", "b:a", "c"}, []domain.LegID{"a", "b", "c"}},
		{"one leg", []string{"only"}, []domain.LegID{"only"}},
		{"no legs", nil, []domain.LegID{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Order(legs(tc.legs...))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("order = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrderRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		legs []string
		want string
	}{
		{"a cycle of two", []string{"pay:settled", "settled:pay"}, "needs has a cycle: pay, settled, pay"},
		{"a cycle behind a leg", []string{"a", "b:a c", "c:b"}, "needs has a cycle: b, c, b"},
		{"a long cycle", []string{"a:c", "b:a", "c:b"}, "needs has a cycle: a, c, b, a"},
		{"itself", []string{"a:a"}, "needs has a cycle: a, a"},
		{"an unknown leg", []string{"a:x"}, "leg a needs x, which is not a leg of the journey"},
		{"a leg twice", []string{"a", "a"}, "leg a is used twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Order(legs(tc.legs...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("order = %v, err = %v, want %q", got, err, tc.want)
			}
		})
	}
}

// A graph that has no cycle is never refused, whatever the file order: every leg comes after the
// legs it needs.
func TestOrderPlacesNeedsFirst(t *testing.T) {
	j := legs("e:d", "d:b c", "c:a", "b:a", "a")
	got, err := Order(j)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[domain.LegID]int{}
	for i, id := range got {
		pos[id] = i
	}
	for _, l := range j.Legs {
		for _, n := range l.Needs {
			if pos[n] > pos[l.ID] {
				t.Errorf("%s comes before %s, which it needs: %v", l.ID, n, got)
			}
		}
	}
	if len(got) != len(j.Legs) {
		t.Errorf("order = %v", got)
	}
}
