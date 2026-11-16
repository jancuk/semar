# Semar report run-20261114-0001

42 use cases: 35 verified, 2 failed, 3 blocked, 2 need a person
Details: `semar ledger --ticket SHOP-482 --ticket refunds`

- Environment: uat (test)
- Started: 2026-11-14T02:30:00Z
- Ended: 2026-11-14T02:31:35Z
- Builds at the start: orders a1b2c3d config sha256:11aa
- Builds at the end: orders a1b2c3d config sha256:11aa
- Outcomes: failed 1, blocked 1, inconclusive 1, stale 0, not_run 0, passed 2

### SHOP-482-01 Refund above the paid amount is refused

Outcome: **failed**

- Reason: refund-too-much: expected 422, got 201 (step refund)
- Reason: a check with a confirmed source failed (step refund)

- Check refund, check 1: false
  - Expected: status 422
  - Source: ticket, cite: SHOP-482 AC2
  - Observed: status 201

Reproduce: semar run --scenario SHOP-482-01

### SHOP-482-03 Partial refund

Outcome: **blocked**

- Reason: setup step failed: order was not created (step create-order)

Reproduce: semar run --scenario SHOP-482-03

### SHOP-482-02 Order list

Outcome: **inconclusive**

- Reason: a check could not be evaluated (step list)

- Check list, check 1: not_evaluated
  - Expected: \$.items count 3
  - Source: baseline
  - Observed: (empty)
  - Detail: path \$.items not found

Reproduce: semar run --scenario SHOP-482-02

<details>
<summary>Passed (2)</summary>

- SHOP-482-04 Full refund
- SHOP-482-05 Refund of a cancelled order

</details>

## Data touched

- create order ord-1, by SHOP-482-01 step create-order: cleaned up
- create refund rf-9, by SHOP-482-01 step refund: left in place
