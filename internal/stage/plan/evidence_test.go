package plan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/provider"
)

func section(path, body string) string {
	return fmt.Sprintf("diff --git a/%[1]s b/%[1]s\n--- a/%[1]s\n+++ b/%[1]s\n@@ -1 +1 @@\n%[2]s\n", path, body)
}

func fileOf(path, kind string, tickets ...string) domain.FileChange {
	return domain.FileChange{Path: path, Status: "modified", Kind: kind, Added: 1, Deleted: 1, Tickets: tickets}
}

func TestChooseDiffKeepsAWholeDiffUnderTheBudget(t *testing.T) {
	diff := section("a.go", "+x") + section("b.go", "+y")
	got := chooseDiff([]byte(diff), []domain.FileChange{fileOf("a.go", "source", "SHOP-1"), fileOf("b.go", "source")}, len(diff))
	if !got.Whole || got.Text != diff || len(got.Omitted) != 0 {
		t.Errorf("choice = %+v", got)
	}
	if empty := chooseDiff(nil, nil, 10); !empty.Whole || empty.Text != "" {
		t.Errorf("an empty diff = %+v", empty)
	}
}

func TestChooseDiffSpendsTheBudgetOnTheFilesThatMatterMost(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("+x", n) }
	parts := map[string]string{
		"docs/guide.md":          section("docs/guide.md", pad(10)),
		"internal/zebra_test.go": section("internal/zebra_test.go", pad(10)),
		"internal/orders.go":     section("internal/orders.go", pad(40)),
		"internal/huge.go":       section("internal/huge.go", pad(4000)),
		"internal/other.go":      section("internal/other.go", pad(10)),
		".semar/plans/p.md":      section(".semar/plans/p.md", pad(10)),
		"gen/api.pb.go":          section("gen/api.pb.go", pad(10)),
	}
	order := []string{"docs/guide.md", "internal/zebra_test.go", "internal/orders.go", "internal/huge.go", "internal/other.go", ".semar/plans/p.md", "gen/api.pb.go"}
	var diff strings.Builder
	for _, p := range order {
		diff.WriteString(parts[p])
	}
	files := []domain.FileChange{
		fileOf("docs/guide.md", "docs", "SHOP-1"),
		fileOf("internal/zebra_test.go", "test", "SHOP-1"),
		fileOf("internal/orders.go", "source", "SHOP-1"),
		fileOf("internal/huge.go", "source", "SHOP-1"),
		fileOf("internal/other.go", "source"),
		fileOf(".semar/plans/p.md", "docs", "SHOP-1"),
		fileOf("gen/api.pb.go", "generated", "SHOP-1"),
	}
	// Room for the four files that rank first and fit, and for nothing after them.
	budget := len(parts["internal/orders.go"]) + len(parts["internal/zebra_test.go"]) + len(parts["docs/guide.md"]) + 5
	got := chooseDiff([]byte(diff.String()), files, budget)
	if got.Whole {
		t.Fatal("a diff over the budget is marked whole")
	}
	if len(got.Text) > budget {
		t.Errorf("the text is %d bytes, over the budget %d", len(got.Text), budget)
	}
	var want strings.Builder
	for _, p := range []string{"docs/guide.md", "internal/zebra_test.go", "internal/orders.go"} {
		want.WriteString(parts[p])
	}
	if got.Text != want.String() {
		t.Errorf("text:\n%s\nwant the three files that fit, in the order of the diff:\n%s", got.Text, want.String())
	}
	var omitted []string
	for _, line := range got.Omitted {
		omitted = append(omitted, strings.SplitN(line, "`", 3)[1])
	}
	if strings.Join(omitted, " ") != "internal/huge.go internal/other.go .semar/plans/p.md gen/api.pb.go" {
		t.Errorf("omitted = %q", got.Omitted)
	}
	if got.Omitted[0] != "- `internal/huge.go` (+1 -1, source)" {
		t.Errorf("an omitted line = %q", got.Omitted[0])
	}
}

func TestChooseDiffPrefersAFileOfATicketThenSourceThenTheSmallerFile(t *testing.T) {
	big := section("src/a.go", strings.Repeat("+a", 30))
	small := section("src/b.go", strings.Repeat("+b", 10))
	cfg := section("deploy/c.yaml", strings.Repeat("+c", 10))
	unnamed := section("src/d.go", strings.Repeat("+d", 10))
	diff := big + small + cfg + unnamed
	files := []domain.FileChange{
		fileOf("src/a.go", "source", "SHOP-1"), fileOf("src/b.go", "source", "SHOP-1"),
		fileOf("deploy/c.yaml", "config", "SHOP-1"), fileOf("src/d.go", "source"),
	}
	got := chooseDiff([]byte(diff), files, len(big)+len(small))
	if got.Text != big+small {
		t.Errorf("text = %q", got.Text)
	}
	got = chooseDiff([]byte(diff), files, len(small)+len(cfg))
	if got.Text != small+cfg {
		t.Errorf("a larger source file that does not fit must give way to smaller ones: %q", got.Text)
	}
}

func TestSplitDiffMatchesRenamesAndPathsWithSpaces(t *testing.T) {
	rename := "diff --git a/old name.go b/new name.go\nsimilarity index 100%\nrename from old name.go\nrename to new name.go\n"
	quoted := "diff --git \"a/caf\\303\\251.go\" \"b/caf\\303\\251.go\"\n@@ -1 +1 @@\n+x\n"
	files := []domain.FileChange{{Path: "new name.go", OldPath: "old name.go", Status: "renamed", Kind: "source"}}
	got := splitDiff(rename+quoted, files)
	if len(got) != 2 || got[0].file == nil || got[0].file.Path != "new name.go" || got[1].file != nil {
		t.Fatalf("sections = %+v", got)
	}
	if got[0].text != rename || got[1].text != quoted {
		t.Errorf("the sections do not add up to the diff: %q, %q", got[0].text, got[1].text)
	}
	omitted := omittedLine(got[1])
	if !strings.Contains(omitted, "caf") {
		t.Errorf("a section with no file is not named: %q", omitted)
	}
}

func TestFencedCannotBeEndedByItsText(t *testing.T) {
	got := fenced("diff", "+```\n+````\n+ rule\n")
	if !strings.HasPrefix(got, "`````diff\n") || !strings.HasSuffix(got, "\n`````") {
		t.Errorf("fence = %q", got)
	}
}

func TestApiExcerptNamesTheOperationsThatTheDiffMentions(t *testing.T) {
	doc := `openapi: 3.0.3
servers:
  - url: https://orders.example.com/v1
paths:
  /orders:
    get:
      operationId: listOrders
      summary: List the orders
      responses:
        "200": {description: ok}
    post:
      summary: Create an order
      responses:
        "201": {description: created}
  /orders/{orderId}:
    get:
      responses:
        "200": {description: ok}
  /orders/{orderId}/refunds:
    post:
      operationId: refundOrder
      summary: Refund an order
      responses:
        "200": {description: refunded}
        "422": {description: refused}
  /health:
    get:
      operationId: health
      responses:
        "200": {description: ok}
  /:
    get:
      responses:
        "200": {description: root}
`
	src := apiSource(t, "api", doc, "")
	diff := "+\tmux.HandleFunc(\"POST /v1/orders/{id}/refunds\", s.refund)\n" +
		"+\tmux.HandleFunc(\"GET /orders/:id\", s.get)\n" +
		"+\t// health stays as it is\n" +
		"+\tfoo(\"/ordersX\")\n"
	got := apiExcerpt([]knowledge.Source{src}, []byte(diff))
	want := "- `GET /orders/{orderId}`. Declares 200. See `docs/openapi.yaml`:16\n" +
		"- `POST /orders/{orderId}/refunds` (`refundOrder`): Refund an order. Declares 200, 422. See `docs/openapi.yaml`:20"
	if got != want {
		t.Errorf("excerpt:\n%s\nwant:\n%s", got, want)
	}
	// "/orders" ends before "/{id}" and before "X", so neither operation of it is named. The word
	// health in a comment is too short and too plain to be taken for the operation ID health.
	byID := apiExcerpt([]knowledge.Source{src}, []byte("+ func (s *Server) listOrders(w http.ResponseWriter) {\n+ // healthcheck\n"))
	if byID != "- `GET /orders` (`listOrders`): List the orders. Declares 200. See `docs/openapi.yaml`:6" {
		t.Errorf("by ID: %q", byID)
	}
	if none := apiExcerpt([]knowledge.Source{src}, []byte("+ nothing here\n")); none != "" {
		t.Errorf("excerpt of a diff that names no route: %q", none)
	}
}

func TestApiExcerptIsBoundedAndSkipsDocumentsItCannotRead(t *testing.T) {
	var doc, diff strings.Builder
	doc.WriteString("openapi: 3.0.3\npaths:\n")
	for i := 0; i < maxAPIOperations+5; i++ {
		fmt.Fprintf(&doc, "  /r%d/items:\n    get:\n      responses:\n        \"200\": {description: ok}\n", i)
		fmt.Fprintf(&diff, "+ route(\"/r%d/items\")\n", i)
	}
	src := apiSource(t, "api", doc.String(), "")
	got := apiExcerpt([]knowledge.Source{src}, []byte(diff.String()))
	lines := strings.Split(got, "\n")
	if len(lines) != maxAPIOperations+1 || lines[maxAPIOperations] != "- and 5 more operations" {
		t.Errorf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	broken := src
	broken.Problem = "the file is missing"
	if got := apiExcerpt([]knowledge.Source{broken, docsSource}, []byte(diff.String())); got != "" {
		t.Errorf("excerpt of an unusable source: %q", got)
	}
}

func runPrompt(t *testing.T, in Input) string {
	t.Helper()
	s := &scripted{results: []provider.Result{{Output: []byte(goodDraft)}}}
	if _, err := Run(t.Context(), s, in); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 1 {
		t.Fatalf("%d jobs", len(s.jobs))
	}
	return s.jobs[0].Prompt
}

func TestRunPutsTheEvidenceInThePrompt(t *testing.T) {
	in := withDocs(t, input(t))
	in.Knowledge = []knowledge.Source{apiSource(t, "api", fullAPI, "orders"), docsSource}
	in.Change.Files = []domain.FileChange{fileOf("internal/refund.go", "source", "SHOP-482")}
	in.Diff = []byte(section("internal/refund.go", "+\tmux.HandleFunc(\"POST /orders/{id}/refunds\", s.refund)"))
	in.LeftOut = []LeftOut{{Path: "assets/logo.bin", Reason: "larger than 5 MiB"}}

	got := runPrompt(t, in)
	for _, want := range []string{
		"## The difference\n\nThis is the whole difference between the two versions.",
		"```diff\ndiff --git a/internal/refund.go b/internal/refund.go",
		"## The API operations that the difference names",
		"- `POST /orders/{orderId}/refunds`. Declares 201, 4XX. See `docs/openapi.yaml`:",
		"### Files left out of the copy",
		"- `assets/logo.bin`: larger than 5 MiB",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(got, "changes.md") {
		t.Error("the prompt sends the planner to the change file")
	}
}

func TestRunShowsPartOfALargeDiffAndNamesTheRest(t *testing.T) {
	in := input(t)
	in.Change.Files = []domain.FileChange{fileOf("internal/refund.go", "source", "SHOP-482"), fileOf("internal/vendor_dump.go", "source", "SHOP-482")}
	small := section("internal/refund.go", "+\treturn refund(w, r)")
	big := section("internal/vendor_dump.go", strings.Repeat("+"+strings.Repeat("x", 99)+"\n", MaxDiffBytes/100+10))
	in.Diff = []byte(big + small)

	got := runPrompt(t, in)
	for _, want := range []string{
		"The difference is large, so this is the part of it that matters most",
		"+\treturn refund(w, r)",
		"Not shown here:\n\n- `internal/vendor_dump.go` (+1 -1, source)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(got, strings.Repeat("x", 99)) {
		t.Error("a file over the budget is in the prompt")
	}
	if strings.Contains(got, "This is the whole difference") {
		t.Error("a cut diff is called whole")
	}
	if n := len(got); n > MaxDiffBytes+16<<10 {
		t.Errorf("the prompt is %d bytes", n)
	}
}

func TestRunKeepsTheEvidenceOnTheSecondAttempt(t *testing.T) {
	bad := withProblem(t, func(d *Draft) { d.Scenarios[0].Steps[1].Method = "DELETE" })
	s := &scripted{results: []provider.Result{{Output: bad}, {Output: []byte(goodDraft)}}}
	in := input(t)
	in.Change.Files = []domain.FileChange{fileOf("internal/refund.go", "source", "SHOP-482")}
	in.Diff = []byte(section("internal/refund.go", "+\treturn refund(w, r)"))
	if _, err := Run(t.Context(), s, in); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 2 {
		t.Fatalf("%d jobs", len(s.jobs))
	}
	for i, j := range s.jobs {
		if !strings.Contains(j.Prompt, "+\treturn refund(w, r)") {
			t.Errorf("job %d lacks the difference", i+1)
		}
	}
}
