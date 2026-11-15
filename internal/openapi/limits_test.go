package openapi_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/openapi"
)

func TestParseKeepsTheFirst32DistinctPrefixes(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.0\nservers:\n")
	var want []string
	for i := 0; i < 50; i++ {
		// Every address twice, so the duplicates do not count towards the limit.
		fmt.Fprintf(&sb, "  - url: /s%d\n  - url: https://example.com/s%d/\n", i, i)
		if i < 32 {
			want = append(want, fmt.Sprintf("/s%d", i))
		}
	}
	d, err := openapi.Parse([]byte(sb.String()), "x")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Prefixes, want) {
		t.Errorf("Prefixes = %v, want the first 32 distinct ones %v", d.Prefixes, want)
	}
	if m := d.Lookup("GET", "/s40/a"); m.PathKnown {
		t.Errorf("a prefix beyond the limit was used: %+v", m)
	}
}

func TestParseKeepsAtMost32PrefixesInJSON(t *testing.T) {
	var parts []string
	for i := 0; i < 100; i++ {
		parts = append(parts, fmt.Sprintf(`{"url":"/s%d"}`, i))
	}
	d, err := openapi.Parse([]byte(`{"openapi":"3.0.0","servers":[`+strings.Join(parts, ",")+`]}`), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Prefixes) != 32 || d.Prefixes[31] != "/s31" {
		t.Errorf("Prefixes = %v", d.Prefixes)
	}
}

func TestManyServersAndOperationsStayFast(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.0\nservers:\n")
	for i := 0; i < 100_000; i++ {
		fmt.Fprintf(&sb, "  - url: /s%d\n", i)
	}
	sb.WriteString("paths:\n")
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "  /p%d:\n    get: {}\n", i)
	}
	start := time.Now()
	d, err := openapi.Parse([]byte(sb.String()), "x")
	if err != nil {
		t.Fatal(err)
	}
	if m := d.Lookup("GET", "/s3/p4999"); !m.Found {
		t.Errorf("Lookup = %+v", m)
	}
	if m := d.Lookup("GET", "/s90000/p1"); m.PathKnown {
		t.Errorf("Lookup = %+v, want the prefix beyond the limit to be unknown", m)
	}
	for i := 0; i < 100; i++ {
		d.Lookup("POST", "/p77")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Parse and Lookup took %v", took)
	}
}

func TestLookupOnADocumentBuiltByHand(t *testing.T) {
	d := openapi.Document{
		Prefixes: []string{"/v1"},
		Operations: []openapi.Operation{
			{Method: "GET", Path: "/orders/{id}"},
			{Method: "PUT", Path: "/orders/{orderId}"},
		},
	}
	m := d.Lookup("PUT", "/v1/orders/{x}")
	if !m.Found || !reflect.DeepEqual(m.Methods, []string{"GET", "PUT"}) || m.Operation.Path != "/orders/{orderId}" {
		t.Errorf("Lookup = %+v", m)
	}
}

func TestAliasedMappingScansAreCharged(t *testing.T) {
	// Every method of every path item is an alias of one mapping with many keys that
	// ends in responses, so each operation scans the whole mapping.
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.0\npaths:\n  /p0:\n    get: &big\n")
	for i := 0; i < 30_000; i++ {
		fmt.Fprintf(&sb, "      k%d: 1\n", i)
	}
	sb.WriteString("      responses: {}\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "  /p%d:\n", i)
		for _, m := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
			fmt.Fprintf(&sb, "    %s: *big\n", m)
		}
	}
	_, err := openapi.Parse([]byte(sb.String()), "x")
	if err == nil || !strings.Contains(err.Error(), "too large or too repetitive") {
		t.Fatalf("err = %v, want the too large or too repetitive error", err)
	}
}

func TestYAMLLimitIs2MiBAndJSONLimitIs5MiB(t *testing.T) {
	_, err := openapi.Parse([]byte("openapi: 3.0.0\n"+strings.Repeat("#", 2<<20)), "x")
	if err == nil || !strings.Contains(err.Error(), "larger than 2 MiB") || !strings.Contains(err.Error(), "YAML") {
		t.Errorf("yaml: err = %v", err)
	}
	json := func(n int) []byte { return []byte(`{"openapi":"3.0.0"}` + strings.Repeat(" ", n)) }
	if _, err := openapi.Parse(json(3<<20), "x"); err != nil {
		t.Errorf("json of 3 MiB: %v", err)
	}
	_, err = openapi.Parse(json(5<<20), "x")
	if err == nil || !strings.Contains(err.Error(), "larger than 5 MiB") || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("json: err = %v", err)
	}
}

func TestAliasedServerMappingScansAreCharged(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.0\nservers:\n  - &s\n")
	for i := 0; i < 30_000; i++ {
		fmt.Fprintf(&sb, "    k%d: 1\n", i)
	}
	sb.WriteString("    url: /v1\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("  - *s\n")
	}
	_, err := openapi.Parse([]byte(sb.String()), "x")
	if err == nil || !strings.Contains(err.Error(), "too large or too repetitive") {
		t.Fatalf("err = %v", err)
	}
}
