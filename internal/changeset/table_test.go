package changeset

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func sampleChangeSet() domain.ChangeSet {
	at := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	return domain.ChangeSet{
		Base: "master", Head: "release/week-02",
		BaseCommit: "1111111111111111111111111111111111111111",
		HeadCommit: "2222222222222222222222222222222222222222",
		MergeBase:  "1111111111111111111111111111111111111111",
		Commits: []domain.ChangeCommit{
			{ID: "aaaaaaaaaaaaaaaaaaaa", Subject: "SHOP-1 add refund", At: at, Tickets: []string{"SHOP-1"}, Files: []string{"refund.go"}},
			{ID: "bbbbbbbbbbbbbbbbbbbb", Subject: "PAY-7 | audit\nrows", At: at, Tickets: []string{"PAY-7", "SHOP-1"}, Files: []string{"audit.go", "refund.go"}},
			{ID: "cccccccccccccccccccc", Subject: "tidy | notes", At: at, Files: []string{"docs/notes.md"}},
			{ID: "dddddddddddddddddddd", Subject: "Merge side for SHOP-9", At: at, Merge: true, Tickets: []string{"SHOP-9"}},
		},
		Tickets: []domain.TicketChange{
			{Key: "PAY-7", Commits: []string{"bbbbbbbbbbbbbbbbbbbb"}, Files: []string{"audit.go", "refund.go"}},
			{Key: "SHOP-1", Commits: []string{"aaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb"}, Files: []string{"audit.go", "refund.go"}},
			{Key: "SHOP-9", Commits: []string{"dddddddddddddddddddd"}},
		},
		NoTicket: []string{"cccccccccccccccccccc"},
		Files: []domain.FileChange{
			{Path: "audit.go", Status: "added", Kind: "source", Added: 12, Tickets: []string{"PAY-7", "SHOP-1"}},
			{Path: "assets/logo.png", Status: "added", Kind: "source", Binary: true},
			{Path: "docs/notes.md", Status: "modified", Kind: "docs", Added: 1, Deleted: 2},
			{Path: "new|name.go", OldPath: "old name.go", Status: "renamed", Kind: "source", Tickets: []string{"SHOP-1"}},
			{Path: "old.sql", Status: "deleted", Kind: "source", Deleted: 30},
		},
	}
}

func TestTableGolden(t *testing.T) {
	cases := []struct {
		name   string
		cs     domain.ChangeSet
		golden string
	}{
		{"full", sampleChangeSet(), "table.golden.md"},
		{"empty", domain.ChangeSet{Base: "a", Head: "b", BaseCommit: "1234", HeadCommit: "5678"}, "table_empty.golden.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Table(&buf, c.cs); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", c.golden)
			if *update {
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != string(want) {
				t.Fatalf("table differs from %s (run with -update to rewrite it)\ngot:\n%s\nwant:\n%s", path, got, want)
			}
		})
	}
}

func TestTableEscapes(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sampleChangeSet()); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "| PAY-7") && strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") != 5 {
			t.Errorf("an unescaped pipe splits the row: %s", line)
		}
	}
	if strings.Contains(buf.String(), "audit\nrows") {
		t.Error("a newline survived in a cell")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestTableWriteError(t *testing.T) {
	if err := Table(failWriter{}, sampleChangeSet()); err == nil {
		t.Fatal("a failed write was ignored")
	}
}
