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

Given: order from order.paid

1. POST /v1/orders/{id}/refunds: Request a refund of 150000
   - Expects status 422. Cite: internal/refund/handler.go:41 returns 422 when the amount is above the rest
   - Expects \$.error.code equals REFUND_EXCEEDS_TOTAL. Cite: internal/refund/handler.go:42 names the code

### SHOP-483

Kind: refactor. Risk: low, because a rename.

Behaviour: none

#### SHOP-483-01: An order can be read back

The read must stay the same.

1. POST /v1/orders (setup): Create an order
2. GET /v1/orders/{id}: Read it
   - Expects \$.data.amount equals 5. Cite: internal/orders/read.go:3 returns the amount

#### SHOP-483-02: The summary of an order can be read

The read must stay the same.

First drafted after the trial run. Check that its expected results come from the code.

1. POST /v1/orders (setup): Create an order
2. GET /v1/orders/{id}/summary: Read it
   - Expects \$.data.amount equals 5. Cite: internal/orders/read.go:3 returns the amount

## Not drafted

- SHOP-482: a refund on another user's order. Needs: a second test account

## Fixtures needed

None.

## Files that no scenario covers

- internal/refund/audit.go: writes a row that no endpoint returns

## Trial run

Code sent the read-only steps once to local before the plan was written. The environment ran orders 9f3c2ab. A trial result is evidence about a request. It is not a test result, and no expected result was compared. Run r-20261117T081500Z-1a2b holds every request: semar activity r-20261117T081500Z-1a2b.

The planner kept the expected results of each scenario that it kept, matched by ticket and title. A scenario that was renamed counts as a new one.

### SHOP-482-01 Refund above the order total is rejected

- given order (order.paid): find found a record
- refund-too-much: not tried: writes

### SHOP-483-01 An order can be read back

- create-order: not tried: writes
- read-order: GET /v1/orders/{id} answered 200, capture status found

### SHOP-483-02 The summary of an order can be read

- create-order: not tried: writes
- summary: GET /v1/orders/{id}/summary answered 404

### Needs a second look

- SHOP-483-02, step summary: GET /v1/orders/{id}/summary answered 404: the route is not on the environment yet, or the path is wrong

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
- Prompt version: 8

Every expected result here was read from the code by a model. Confirm each scenario before its result counts.

The read-only requests were tried once. The others were not sent before the first run.

A request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value, except an ID that cannot exist, written as `missing-{{run.id}}`, for a not-found test. The planner was told to list every other case under "Not drafted". Code cannot check this, so read the requests before the first run.
