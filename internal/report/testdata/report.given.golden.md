# Semar report r-given

- Environment: local (test)
- Started: 2026-11-17T09:00:00Z
- Ended: 2026-11-17T09:01:00Z
- Builds at the start: none recorded
- Builds at the end: none recorded
- Outcomes: failed 0, blocked 1, inconclusive 0, stale 0, not_run 0, passed 2

### SHOP-482-07 Refund before approval

Outcome: **blocked**

- Reason: fixture order.paid is not approved: run semar confirm --fixture order.paid

- Given order: order.paid 1.0.0 (hash cccccccccccc), no state was given; cleanup not needed

Reproduce: semar run --scenario SHOP-482-07

<details>
<summary>Passed (2)</summary>

- SHOP-482-05 Refund of a paid order
  - Given order: order.paid 1.0.0 (hash aaaaaaaaaaaa), made by create; cleanup failed
- SHOP-482-06 Refund of a found order
  - Given order: order.paid 1.0.0 (hash bbbbbbbbbbbb), found by find; cleanup not needed

</details>

## Data touched

Nothing was written in the environment.

## Still exists

- SHOP-482-05: order created by fixture order.paid (as order) may still exist: cleanup step 1 (POST /v1/orders/{{id}}/cancel) answered 500
