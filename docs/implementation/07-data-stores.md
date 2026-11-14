# Plan 07: Data stores

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Database evidence](../TRD.md#database-evidence)
* **Needs first:** Plan 04. Spike S9.
* **Requirements:** FR-81 to 88, 180 to 182

**Goal.** A scenario can check a row or a document when no API read can verify the result. Every connection to a data store is read-only.

**What works when it is done.** A `db` check in a scenario passes or fails on PostgreSQL, MySQL, and Elasticsearch, with the query and the rows stored as evidence.

## Architecture

```text
check with db:  -->  gate (parsed statement, allowlists)  -->  Store.Query (read-only session)  -->  rows  -->  redaction  -->  recorder
                                                                      ^
semar doctor  -->  Store.Privileges  -->  an account that can write: every check on that store is refused
```

* Four layers keep a store read-only. The first two are enforced by the store itself and hold even if Semar has a defect. See the TRD, [Read-only, in four layers](../TRD.md#read-only-in-four-layers).
* A store is a port (X1). A company with another engine adds an adapter that supplies `Privileges`, `Describe`, `Query`, and a gate.
* The agent gets a store's name, never its address or its credentials (A5).

**Ports added:** data store.

## File structure

```text
internal/engine/db/db.go           the Store port, Query, Rows, Privileges, the registry
internal/engine/db/gate.go         shared allowlist logic and limits
internal/engine/db/postgres/       store.go, gate.go
internal/engine/db/mysql/          store.go, gate.go
internal/engine/db/elastic/        store.go, gate.go
internal/engine/db/dbtest/         the conformance kit, and the table of statements that must be refused
internal/check/db.go               evaluate a db check, with "eventually"
internal/mcpserver/tools_db.go     db_describe, db_query, es_describe, es_search
```

## Interfaces

```go
// internal/engine/db
type Privileges struct {
	CanWrite bool
	Detail   []string // the grants or privileges that allow a write
}
type Schema struct {
	Tables []Table // or indices
}
type Table struct {
	Name    string
	Columns []Column // name and type. Denied columns are left out
}
type Query struct {
	Store     string
	Statement string          // SQL with $1 or ? placeholders. Empty for a search
	Params    []any
	Index     string          // for a search
	Search    json.RawMessage // the query body of a search
	MaxRows   int
	Timeout   time.Duration
}
type Rows struct {
	Columns   []string
	Values    [][]any // after the row limit and the column deny list
	Truncated bool
}

type Store interface {
	Kind() string // "postgres" | "mysql" | "elastic"
	Replica() bool
	Privileges(ctx context.Context) (Privileges, error)
	Describe(ctx context.Context) (Schema, error)
	// Gate refuses a query before any connection is used.
	Gate(q Query) error
	Query(ctx context.Context, q Query) (Rows, error)
}

type Refusal struct{ Reason, Detail string }
func (r *Refusal) Error() string
```

## Formats

**A database check in a scenario.**

```yaml
- id: settlement-row-written
  action: Check that the settlement worker recorded the refund
  expect:
    - check:
        db:
          store: orders-db
          query: "select status from refund_events where refund_id = $1"
          params: ["{{refund_id}}"]
          rows: 1
          column: status
          equals: SETTLED
          eventually: 20s
          noApiReason: refund_events has no read endpoint
      source: person
```

For a search, `index` and `search` replace `query`, and `column` is a JSONPath into the first hit.

**Configuration.**

```yaml
databases:
  orders-db:
    kind: postgres
    dsn: "keychain:semar/uat-orders-dsn"
    replica: true
    tables: [orders, refunds, refund_events]
    denyColumns: [customers.national_id, customers.phone]
    functions: [count, coalesce, lower, now]
    maxRows: 50
    timeout: 5s
  search:
    kind: elastic
    url: https://es.uat.example.com
    apiKey: "keychain:semar/uat-es-key"
    indices: [orders-v3]
    denyFields: [customer.national_id]
    maxHits: 50
    timeout: 5s
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | `Privileges` reads what the account may do from the store's own catalog or privilege call. It never tests a privilege by trying to write | TRD, layer 1 |
| R2 | When `CanWrite` is true, every check and every tool call on that store is refused, and `semar doctor` names the grants that allow a write | FR-84, 182 |
| R3 | PostgreSQL and MySQL: each query runs in a read-only transaction with a statement timeout. A replica is used when one is configured | TRD, layer 2 |
| R4 | The SQL gate parses the statement. It accepts exactly one `SELECT`. It refuses a second statement, a data-modifying clause in `WITH`, `SELECT ... INTO`, a locking clause, a function that is not on the allowlist, and a table that is not on the allowlist | TRD, layer 3 |
| R5 | The search gate accepts an index on the allowlist and a body with no `script` key at any depth. The adapter builds the request itself and sends only a search, a count, or a read by ID. A tool call cannot name a path or a method | TRD, the four layers per engine |
| R6 | A wildcard index is accepted only when every index it can match is on the allowlist | FR-181 |
| R7 | Results are cut at `maxRows`, denied columns and fields are removed, and the rest passes through redaction before it is stored or returned | TRD, layer 4 |
| R8 | A `db` check must have `noApiReason`. When an OpenAPI file has a `GET` path whose response schema holds the checked column name, the plan carries a warning, and nothing is blocked | FR-82 |
| R9 | `eventually` repeats the query until the check holds or the time runs out, with a growing pause from 250 milliseconds to 2 seconds. Each attempt is stored | FR-86, Q3 |
| R10 | A check whose time ran out is `false` on a primary and `not_evaluated` on a replica, because the replica may be behind | TRD, Evidence and outcome |
| R11 | A store that cannot be reached makes the scenario `blocked` | TRD, Evidence and outcome |
| R12 | The stored evidence holds the query text, the parameters, the redacted rows, the time, and the store's name. It never holds the connection string | FR-85, 88 |
| R13 | Each report counts the checks that used a data store | FR-87 |
| R14 | The gate and the parser have fuzz tests. A statement that the gate accepts must parse as one `SELECT` | G16 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | The port, shared limits, and the kit | `db/db.go`, `gate.go`, `dbtest` | The kit holds the table of statements and search bodies that must be refused, and a fake store |
| 2 | PostgreSQL gate | `db/postgres/gate.go` | The refusal table for the dialect. A fuzz test (R14) |
| 3 | PostgreSQL store | `db/postgres/store.go` | Against a container: a read-only account passes the kit. A write-capable account gives `CanWrite` with the grant named. A write inside an allowed function is stopped by the read-only transaction |
| 4 | MySQL gate and store | `db/mysql/*` | The same two sets of tests for the dialect |
| 5 | Elasticsearch gate and store | `db/elastic/*` | The refusal table: `_update`, `_bulk`, `_delete_by_query`, a script in the body, an unlisted index, and a wildcard that reaches one (R5, R6). Against a container: a read-only key passes, and a key that can write is refused |
| 6 | The db check | `check/db.go` | A row that appears after two attempts passes. A row that never appears is `false` on a primary and `not_evaluated` on a replica (R10). A redacted value is `not_evaluated` |
| 7 | Tools | `mcpserver/tools_db.go` | `db_describe` leaves out denied columns. `db_query` goes through the gate. A tool result holds no connection string. `es_search` cannot be given a path |
| 8 | Doctor, the plan warning, and the report count | `cli/doctor.go`, `stage/plan/validate.go`, `report` | Doctor prints the store's state. A check on a column that an OpenAPI read returns carries the warning (R8). The count appears in the report |

## Done when

A scenario with a `db` check passes against a PostgreSQL container, a MySQL container, and an Elasticsearch container, each with a read-only account. With a write-capable account on any of the three, Semar refuses every check on that store and the scenario is `blocked`.

The container tests carry a build tag and run in CI, not in `make check`.

## Review focus

1. **An account that is read-only today and gains a grant tomorrow.** Privileges are read at the start of every run, not once. Task 3.
2. **A function with a side effect on the allowlist.** The read-only transaction still stops the write, and the test shows it. Task 3.
3. **A query that returns a million rows.** The limit is applied in the query where the dialect allows it, and again on the result. Task 1.
4. **Personal data in a column that nobody listed.** Redaction patterns still apply, and `semar doctor` warns when no column is denied on a store. Task 8.
5. **A slow query on a shared test database.** The statement timeout ends it, and the check is `not_evaluated` with the reason. Task 6.

## Open points

* Spike S9 chooses the SQL parsers. A parser that needs cgo breaks G18, so a dialect with no pure-Go parser is not supported, and the reason is recorded.
* How to read privileges on OpenSearch is not verified. Until it is, an OpenSearch store is refused with a message that says so.
