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

Given: buyer from customer.verified (not approved yet: semar confirm --fixture customer.verified)

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

## Not drafted

- SHOP-482: a refund on another user's order. Needs: a second test account
- SHOP-482: a refund on a partly refunded order. Needs: fixture order.refunded
- SHOP-483: the address list. Needs: A fixture: fixture customer.two-addresses.

## Fixtures needed

- SHOP-482: an order that already has one refund. Proposed fixture order.refunded: A paid order with one refund of 100. Parameters: customer. Outputs: id. Services: orders.
  A person writes the fixture in .semar/fixtures/order.refunded.yaml, approves it, and plans again.
- SHOP-483: a customer with two addresses. Proposed fixture customer.two-addresses: A customer who has two saved addresses. Parameters: none. Outputs: id, email. Services: orders.
  A person writes the fixture in .semar/fixtures/customer.two-addresses.yaml, approves it, and plans again.

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
- Prompt version: 4

Every expected result here was read from the code by a model. Confirm each scenario before its result counts.

The requests were drafted from the source and were not sent before the first run.

A request uses a literal ID only when the same scenario captured it or the source holds it as a fixed test value. The planner was told to list every other case under "Not drafted". Code cannot check this, so read the requests before the first run.
