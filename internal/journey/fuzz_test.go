package journey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

// FuzzParse: Parse and Validate never panic. A journey that both accept has an order that holds
// every leg once, and every leg gives a scenario with a valid ID and a script bound to it.
func FuzzParse(f *testing.F) {
	seed, err := os.ReadFile(filepath.Join("testdata", exampleName))
	if err != nil {
		f.Fatal(err)
	}
	s := string(seed)
	for _, text := range []string{
		s,
		"",
		"apiVersion: semar/v1\nkind: Journey\n",
		strings.Replace(s, "amount: 1000", "amount: 1e3", 1),
		strings.Replace(s, "amount: 1000", "amount: 9007199254740993", 1),
		strings.Replace(s, "amount: 1000", "amount: [1, {a: 2.5}, null]", 1),
		strings.Replace(s, "equals: paid }, timeout", "equals: [1] }, timeout", 1),
		strings.Replace(s, "equals: paid }, timeout", "equals: 2.5 }, timeout", 1),
		strings.Replace(s, "timeout: 30s", "timeout: 99h", 1),
		strings.Replace(s, "timeout: 30s", "timeout: 30", 1),
		strings.Replace(s, "needs: [place]", "needs: [settled]", 1),
		strings.Replace(s, "needs: [place]", "needs: [pay]", 1),
		strings.Replace(s, "needs: [place]", "needs: [place, place]", 1),
		strings.Replace(s, "surface: api", "surface: web", 1),
		strings.Replace(s, "surface: api\n    service: payments", "surface: ios\n    given: []\n    service: payments", 1),
		strings.Replace(s, "{{place.id}}", "{{settled.id}}", 1),
		strings.Replace(s, "{{place.id}}", "{{secret.TOKEN}}", 1),
		strings.Replace(s, "{{place.id}}", "{{a.b.c}}", 1),
		strings.Replace(s, "{{place.id}}", "{{place.id", 1),
		strings.Replace(s, "/v1/orders", "/v1/%2e%2e/orders", 1),
		strings.Replace(s, "{{pay.amount}}", "total {{pay.amount}}", 1),
		strings.Replace(s, "legs:\n", "given:\n  - fixture: order.paid\n    as: order\n  - fixture: order.paid\n    as: place\nlegs:\n", 1),
		strings.Replace(s, "id: checkout", "id: check.out", 1),
		strings.Replace(s, "outputs: [id, amount]", "outputs: [id, id, total]", 1),
		"a: &a [*a]\n",
		"a: &a {b: *a}\n",
		"legs: &l [*l]\n",
		"legs: [{needs: [a]}, {id: a, needs: [a]}]\n",
		strings.Repeat("[", 500) + strings.Repeat("]", 500),
		"apiVersion: [1\n",
		"\x00\xff",
	} {
		f.Add([]byte(text))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		j, err := Parse(data, "f.yaml")
		if err != nil {
			return
		}
		ps := Validate(j, orderPaid)
		// The problems that follow a decode must be printable and name a place.
		for _, p := range ps {
			_ = p.String()
		}
		if len(ps) != 0 {
			return
		}
		order, err := Order(j)
		if err != nil {
			t.Fatalf("an accepted journey has no order: %v", err)
		}
		if len(order) != len(j.Legs) {
			t.Fatalf("order %v does not hold the %d legs", order, len(j.Legs))
		}
		placed := map[domain.LegID]bool{}
		for _, id := range order {
			sc, sr := LegScenario(j, id, j.AuthProfile)
			if err := idcheck.Check("scenario", string(sc.ID)); err != nil || len(sc.ID) > maxScenarioID {
				t.Fatalf("leg scenario %q: %v", sc.ID, err)
			}
			if sr.Scenario != sc.ID || len(sr.ExpectHash) != 64 {
				t.Fatalf("script %+v does not stand for scenario %s", sr, sc.ID)
			}
			for _, l := range j.Legs {
				if l.ID == id {
					for _, n := range l.Needs {
						if !placed[n] {
							t.Fatalf("leg %s comes before %s, which it needs: %v", id, n, order)
						}
					}
				}
			}
			placed[id] = true
		}
	})
}
