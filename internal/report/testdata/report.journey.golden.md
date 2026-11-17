# Semar report r-journey

3 use cases: 2 verified, 1 failed
Details: `semar status --ticket SHOP-482`

## Journeys

### checkout An order is placed, paid, and \*shown\* as paid

Outcome: **failed**. First failing leg: settled.

- Reason: leg settled failed: waited 30s and \$.data.status was never paid (6 attempts)
- Reason: invariant 1 is false: 1000, 1000, 999
- Manifest: orders 9f3c2ab config sha256:11aa; payments c0ffee1 config sha256:22bb (hash a901ca967a71)
- Leg place: passed
- Leg pay: passed
- Leg settled: failed: waited 30s and \$.data.status was never paid (6 attempts)
- Invariant 1: false: 1000, 1000, 999

Reproduce: semar journey run checkout --env local

### refund A refund

Outcome: **passed**

- Manifest: orders 9f3c2ab config sha256:11aa (hash 69ea2865b0ae)
- Leg ask: passed

Reproduce: semar journey run refund --env local

- Environment: local (test)
- Started: 2026-11-17T09:00:00Z
- Ended: 2026-11-17T09:01:00Z
- Builds at the start: orders 9f3c2ab config sha256:11aa; payments c0ffee1 config sha256:22bb
- Builds at the end: orders 9f3c2ab config sha256:11aa; payments c0ffee1 config sha256:22bb
- Outcomes: failed 1, blocked 0, inconclusive 0, stale 0, not_run 0, passed 3

### checkout.settled An order is placed: settled

Outcome: **failed**

- Reason: waited 30s and \$.data.status was never paid (6 attempts) (step read)

Reproduce: semar journey run checkout --env local

<details>
<summary>Passed (3)</summary>

- checkout.place
- checkout.pay
- refund.ask

</details>

## Data touched

Nothing was written in the environment.
