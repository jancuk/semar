package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

const ticketFile = "# SHOP-482 Limit a refund\n\n## Acceptance criteria\n\n1. A refund larger than the paid amount is refused.\n"

const refundID = "SHOP-482-01"

const orderPaidFixture = `apiVersion: semar/v1
kind: Fixture
id: order.paid
description: A paid order of 1000 that belongs to the given customer
version: 1.0.0
owner: team-orders
params:
  customer: { default: "{{data.customers.default}}" }
find:
  - request: { service: orders, method: GET, path: /v1/orders, query: { status: paid, customer: "{{customer}}" } }
    captures: [{ name: id, jsonpath: "$.data[0].id" }]
create:
  - request: { service: orders, method: POST, path: /v1/orders, json: { amount: 1000, customer: "{{customer}}", ref: "semar-{{run.id}}" } }
    captures: [{ name: id, jsonpath: "$.data.id" }]
  - request: { service: payments, method: POST, path: /v1/payments, json: { orderId: "{{id}}" } }
  - request: { service: orders, method: GET, path: "/v1/orders/{{id}}" }
    until: { jsonpath: "$.data.status", equals: paid, timeout: 30s, every: 1s }
outputs: [id]
cleanup:
  - request: { service: orders, method: POST, path: "/v1/orders/{{id}}/cancel" }
`

func (r *repo) writeFixture(t *testing.T, name, text string) {
	t.Helper()
	dir := r.path(".semar", "fixtures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustSingleDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	return entries[0].Name()
}

func (p *planRepo) latestRunID(t *testing.T) string {
	t.Helper()
	dirs := p.runDirs(t)
	if len(dirs) == 0 {
		t.Fatal("no run was stored")
	}
	return dirs[len(dirs)-1]
}
