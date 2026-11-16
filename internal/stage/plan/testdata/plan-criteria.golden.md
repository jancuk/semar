# Test plan for release at bbbbbbbbbbbb

Refunds above the order total are now rejected, and an order can be read.

## The change

Base main (aaaaaaaaaaaa), head release (bbbbbbbbbbbb). 2 commits, 2 tickets, 0 without a ticket, 2 files.

### Tickets

| Ticket | Commits | Files | First subject |
| --- | ---: | ---: | --- |
| SHOP-482 | 1 | 1 | SHOP-482 reject a refund above the total |
| SHOP-483 | 1 | 1 | SHOP-483 rename the reader |

### Commits with no ticket

Every commit names a ticket.

### Files

| Path | Status | Kind | + | - | Tickets |
| --- | --- | --- | ---: | ---: | --- |
| internal/orders/read.go | modified | source | +2 | -1 | SHOP-483 |
| internal/refund/handler.go | modified | source | +9 | -0 | SHOP-482 |

## Tickets

### SHOP-482

Kind: behaviour. Risk: standard, because money moves.

Behaviour: a refund above the total gets 422

#### SHOP-482-01: Refund above the order total is rejected

Before: accepted. Now: 422.

1. POST /v1/orders (setup): Create a paid order for 100000
2. POST /v1/orders/{id}/refunds: Request a refund of 150000
   - Expects status 422. Source: ticket. Cite: docs/tickets/SHOP-482.md:6 criterion 1
   - Expects \$.error.code equals REFUND_EXCEEDS_TOTAL. Source: ticket. Cite: docs/tickets/SHOP-482.md:7 criterion 2

Manual checks for a person:

1. The refused refund shows an error banner. Cite: docs/tickets/SHOP-482.md:8 criterion 3
   - Open the order in the app.
   - Request a refund of more than the total.
   - Expected: A red banner says the refund is above the total.

2. Another banner. Cite: docs/tickets/SHOP-482.md:9 criterion 4
   - Open the order in the app.
   - Request a refund of more than the total.
   - Expected: A red banner says the refund is above the total.

Acceptance criteria:

| Line | No. | Criterion | Cited by |
| --- | --- | --- | --- |
| 6 | 1 | Criterion number 1. | SHOP-482-01 |
| 7 | 2 | Criterion number 2. | SHOP-482-01 |
| 8 | 3 | Criterion number 3. | manual check: The refused refund shows an error banner |
| 9 | 4 | Criterion number 4. | manual check: Another banner |
| 10 | 5 | Criterion number 5. | not drafted: criterion 5 |
| 11 | 6 | Criterion number 6. | not drafted: criterion 6 |
| 12 | 7 | Criterion number 7. | not drafted: criterion 7 |
| 13 | 8 | Criterion number 8. | not accounted for |
| 14 | 9 | Pipes \| &lt;b&gt;and&lt;/b&gt; \`ticks\` on a second line. | not accounted for |
| 16 | 10 | Criterion number 10. | not accounted for |

### SHOP-483

Kind: refactor. Risk: low, because a rename.

Behaviour: none

#### SHOP-483-01: An order can be read back

The read must stay the same.

1. POST /v1/orders (setup): Create an order
2. GET /v1/orders/{id}: Read it
   - Expects \$.data.amount equals 5. Cite: internal/orders/read.go:3 returns the amount

## Not drafted

- SHOP-482: a refund on another user's order. Needs: a second test account
- SHOP-482: criterion 5. Needs: a fixture
- SHOP-482: criterion 6. Needs: a fixture
- SHOP-482: criterion 7. Needs: a fixture

## Fixtures needed

None.

## Files that no scenario covers

- internal/refund/audit.go: writes a row that no endpoint returns

## Files left out of the copy

The planner could not read these files of the head commit.

- assets/logo.png: larger than 5 MiB
- vendor/lib: a submodule

## How this plan was made

- Provider: claude (claude)
- Model: model-x
- Reasoning: high
- Attempts: 2 of 3
- Tokens: 1234 input, 56 output
- Setup: the planner could use 2 MCP servers and 3 skills of your own setup, under the permission mode default
- Tool calls: Read 7, MCP tools (names withheld) 3
- Refused tool calls: 2
- Prompt version: 6

Every expected result here was read from the code by a model. Confirm each scenario before its result counts.

The requests were drafted from the source and were not sent before the first run.

A request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value. The planner was told to list every other case under "Not drafted". Code cannot check this, so read the requests before the first run.
