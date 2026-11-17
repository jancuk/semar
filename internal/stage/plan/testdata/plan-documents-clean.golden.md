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

## Not drafted

- SHOP-482: a refund on another user's order. Needs: a second test account

## Fixtures needed

None.

## Files that no scenario covers

- internal/refund/audit.go: writes a row that no endpoint returns

## Documents

The planner was told to read these before it drafted.

- api: the API document docs/openapi.yaml (OpenAPI 3.0.3, 3 operations)
- docs: the documents under docs (2 files)

### Compared with the API document

Code compared every drafted request with the API document.

No drafted request differs from the API document.

### Where the planner says a document and the code differ

The planner reported these. Code checked that both places exist, and did not check that they differ.

The planner reported no difference.

## Files left out of the copy

The planner could not read these files of the head commit.

- assets/logo.png: larger than 5 MiB
- vendor/lib: a submodule

## How this plan was made

- Provider: planner (harness)
- Model: model-x
- Reasoning: high
- Attempts: 2 of 3
- Tokens: 1234 input, 56 output
- Setup: the planner could use 2 MCP servers and 3 skills of your own setup, under the permission mode default
- Tool calls: Read 7, MCP tools (names withheld) 3
- Refused tool calls: 2
- Prompt version: 9

Every expected result here was read from the code by a model. Confirm each scenario before its result counts.

The requests were drafted from the source and were not sent before the first run.

A request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value, except an ID that cannot exist, written as `missing-{{run.id}}`, for a not-found test. The planner was told to list every other case under "Not drafted". Code cannot check this, so read the requests before the first run.
