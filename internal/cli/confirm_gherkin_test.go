package cli_test

import (
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

func TestConfirmShowsTheScenarioAsGherkinWithItsCites(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	res := r.must(t, cli.ExitOK, "confirm", scenarioID, "--yes")
	contains(t, "stdout", res.stdout,
		".semar/scenarios/SHOP-482/"+scenarioID+".feature as Gherkin, with the source and cite of each expected result under it:",
		"    Feature: SHOP-482\n",
		"    @id:"+scenarioID+" @ticket:SHOP-482 @surface:api @auth:support-agent @intent-newline\n",
		"    Scenario: Refund above the order total is rejected\n",
		"    When Request a refund of 150000 for that order\n",
		"    # step: refund-too-much; POST /v1/orders/{id}/refunds\n",
		"    Then the status is 422\n",
		"    # ticket SHOP-482: a refund above the order total must be rejected\n",
		"has 1 expected results that a person must confirm:",
	)
}
