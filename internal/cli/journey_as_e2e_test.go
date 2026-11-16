package cli_test

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

// memberJourney is read by a member who does not own the order: leg place is the owner's, and leg
// member acts as bob.
const memberJourney = `apiVersion: semar/v1
kind: Journey
id: member
title: A member cannot read the owner's order
owner: team-commerce
criticality: standard
legs:
  - id: place
    surface: api
    service: orders
    steps:
      - id: create
        action: Create an order of 1000
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { amount: 1000 } }
        captures: [{ name: id, jsonpath: "$.data.id" }]
        expect: [{ check: { status: 201 }, source: SOURCE }]
    outputs: [id]
  - id: member
    surface: api
    service: orders
    as: ACCOUNT
    needs: [place]
    steps:
      - id: read
        action: Read the order as a member
        hint: { method: GET, path: "/v1/orders/{id}" }
        request: { method: GET, path: "/v1/orders/{{place.id}}" }
        expect: [{ check: { status: 403 }, source: SOURCE }]
`

const bobEnv = "SEMAR_SAMPLE_TOKEN_BOB"

// memberRepo is the journey repository with the account bob in the configuration and the journey
// member, whose leg member acts as account. The orders service records the session of each request.
func memberRepo(t *testing.T, account, source string) (*repo, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var sessions []string
	r := newRepo(t, repoOptions{tree: "e2e-journeys", payments: &sampleapp.PaymentOptions{}, wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasPrefix(req.URL.Path, "/v1/") {
				mu.Lock()
				sessions = append(sessions, req.Header.Get("Authorization"))
				mu.Unlock()
			}
			next.ServeHTTP(w, req)
		})
	}})
	t.Setenv(bobEnv, "tok-bob")
	r.rewriteConfig(t, func(s string) string {
		return s + "  bob:\n    session: {header: Authorization, value: \"Bearer {{secret.TOKEN}}\"}\n    secrets: {TOKEN: \"env:" + bobEnv + "\"}\n"
	})
	text := strings.NewReplacer("ACCOUNT", account, "SOURCE", source).Replace(memberJourney)
	r.writeRel(t, ".semar/journeys/member.yaml", text)
	return r, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sessions)
	}
}

func TestJourneyLegActsAsAnotherAccount(t *testing.T) {
	r, sessions := memberRepo(t, "bob", "person")
	r.must(t, cli.ExitOK, "journey", "run", "member", "--env", "local")
	if got, want := sessions(), []string{"Bearer tok-alice", "Bearer tok-bob"}; !slices.Equal(got, want) {
		t.Errorf("orders saw %v, want the owner's session for the leg place and bob's for the leg member", got)
	}
	j := singleJourney(t, r)
	for _, l := range j.Legs {
		var want []string
		if l.Leg == "member" {
			want = []string{"bob"}
		}
		if !slices.Equal(l.As, want) {
			t.Errorf("leg %s acted as %v, want %v", l.Leg, l.As, want)
		}
	}
	r.assertNoSecret(t, []string{"tok-alice", "tok-bob"})
}

func TestJourneyLegActsAsAnAccountThatIsNotConfigured(t *testing.T) {
	r, sessions := memberRepo(t, "dave", "person")
	res := r.must(t, cli.ExitUndecided, "journey", "run", "member", "--env", "local")
	contains(t, "the output", res.stdout+res.stderr, "account dave is not in the configuration")
	if n := len(sessions()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if j := singleJourney(t, r); j.Outcome != domain.JourneyBlocked {
		t.Errorf("journey %s, want blocked", j.Outcome)
	}
}

func TestJourneyConfirmationShowsWhoALegActsAs(t *testing.T) {
	r, _ := memberRepo(t, "bob", "diff")
	res := r.must(t, cli.ExitOK, "confirm", "--journey", "member", "--yes")
	contains(t, "the confirmation", res.stdout, "Leg member of journey member (scenario member.member), acts as bob", "step read as bob")
	r.must(t, cli.ExitOK, "journey", "run", "member", "--env", "local")
}
