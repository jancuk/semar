package agentloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newWorkspace(t *testing.T) (*workspace, string) {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "work")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(parent, "outside.txt"), "outside secret\n")
	ws, err := openWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.close)
	return ws, dir
}

func run(t *testing.T, ws *workspace, name, args string) (string, bool) {
	t.Helper()
	return ws.call(context.Background(), call("id", name, args))
}

func mustFail(t *testing.T, ws *workspace, name, args, want string) {
	t.Helper()
	text, isErr := run(t, ws, name, args)
	if !isErr || !strings.Contains(text, want) {
		t.Errorf("%s %s = %q, error %v; want an error with %q", name, args, text, isErr, want)
	}
	if strings.Contains(text, "outside secret") {
		t.Errorf("%s %s read a file outside the working directory", name, args)
	}
}

func TestReadFileConfinement(t *testing.T) {
	ws, dir := newWorkspace(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(dir, "up")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("self", filepath.Join(dir, "self")); err != nil {
		t.Fatal(err)
	}
	mustFail(t, ws, "read_file", `{"path":"../outside.txt"}`, "cannot open")
	mustFail(t, ws, "read_file", fmt.Sprintf(`{"path":%q}`, outside), "cannot open")
	mustFail(t, ws, "read_file", `{"path":"link.txt"}`, "cannot open")
	mustFail(t, ws, "read_file", `{"path":"up/outside.txt"}`, "cannot open")
	mustFail(t, ws, "read_file", `{"path":"self"}`, "cannot open")
	mustFail(t, ws, "read_file", `{"path":"."}`, "is a directory")
	mustFail(t, ws, "read_file", `{"path":""}`, "needs a path")
}

func TestWalkFollowsNoLink(t *testing.T) {
	ws, dir := newWorkspace(t)
	write(t, filepath.Join(dir, "a.txt"), "needle\n")
	for name, target := range map[string]string{"loop": ".", "up": "..", "self": "self", "out.txt": filepath.Join(filepath.Dir(dir), "outside.txt")} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	text, isErr := run(t, ws, "list_files", `{}`)
	if isErr || text != "a.txt\n" {
		t.Errorf("list_files = %q", text)
	}
	text, isErr = run(t, ws, "search", `{"pattern":"needle|secret"}`)
	if isErr || text != "a.txt:1: needle\n" {
		t.Errorf("search = %q", text)
	}
}

func TestReadFileLines(t *testing.T) {
	ws, dir := newWorkspace(t)
	write(t, filepath.Join(dir, "a.txt"), "one\r\ntwo\n\nfour")
	write(t, filepath.Join(dir, "empty.txt"), "")
	text, _ := run(t, ws, "read_file", `{"path":"a.txt"}`)
	if text != "1\tone\n2\ttwo\n3\t\n4\tfour\n" {
		t.Errorf("whole file = %q", text)
	}
	text, _ = run(t, ws, "read_file", `{"path":"./a.txt","offset":"2","limit":1}`)
	if text != "2\ttwo\n[more lines follow; continue with offset 3]\n" {
		t.Errorf("one line = %q", text)
	}
	text, _ = run(t, ws, "read_file", `{"path":"a.txt","offset":9}`)
	if !strings.Contains(text, "has 4 lines; offset 9 is past the end") {
		t.Errorf("past the end = %q", text)
	}
	text, _ = run(t, ws, "read_file", `{"path":"empty.txt"}`)
	if !strings.Contains(text, "is empty") {
		t.Errorf("empty = %q", text)
	}
	mustFail(t, ws, "read_file", `{"path":"a.txt","offset":1.5}`, "not a whole number")
}

func TestReadFileCaps(t *testing.T) {
	ws, dir := newWorkspace(t)
	long := strings.Repeat("é", 3000) // 6000 bytes, over the line cap
	write(t, filepath.Join(dir, "long.txt"), long+"\nnext\n")
	text, _ := run(t, ws, "read_file", `{"path":"long.txt"}`)
	first, _, _ := strings.Cut(text, "\n")
	if !strings.HasSuffix(first, " [line cut]") || len(first) > maxLineBytes+40 || !strings.Contains(text, "2\tnext") {
		t.Errorf("long line: %d bytes, %q...", len(first), text[:40])
	}

	var b strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&b, "line %d %s\n", i+1, strings.Repeat("x", 40))
	}
	write(t, filepath.Join(dir, "big.txt"), b.String())
	text, _ = run(t, ws, "read_file", `{"path":"big.txt","limit":100000}`)
	if len(text) > maxResultBytes || !strings.Contains(text, "[stopped at 256 KiB; continue with offset ") {
		t.Errorf("big file: %d bytes, ends %q", len(text), text[len(text)-80:])
	}
	text, _ = run(t, ws, "read_file", `{"path":"big.txt"}`)
	if !strings.HasSuffix(text, "[more lines follow; continue with offset 2001]\n") {
		t.Errorf("default limit ends %q", text[len(text)-80:])
	}
}

func TestBinaryFiles(t *testing.T) {
	ws, dir := newWorkspace(t)
	write(t, filepath.Join(dir, "image.png"), "PNG\x00\x01needle")
	write(t, filepath.Join(dir, "text.txt"), "needle\n")
	mustFail(t, ws, "read_file", `{"path":"image.png"}`, "is a binary file")
	text, _ := run(t, ws, "search", `{"pattern":"needle"}`)
	if text != "text.txt:1: needle\n" {
		t.Errorf("search = %q", text)
	}
}

func TestSearchCapsAndSkips(t *testing.T) {
	ws, dir := newWorkspace(t)
	var b strings.Builder
	for range 300 {
		b.WriteString("hit " + strings.Repeat("y", 500) + "\n")
	}
	write(t, filepath.Join(dir, "many.txt"), b.String())
	write(t, filepath.Join(dir, "huge.txt"), strings.Repeat("hit\n", (maxSearchFileBytes/4)+10))
	text, isErr := run(t, ws, "search", `{"pattern":"^hit"}`)
	if isErr {
		t.Fatal(text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	hits := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "many.txt:") {
			hits++
			if len(l) > len("many.txt:300: ")+maxHitBytes+3 {
				t.Errorf("hit not cut: %d bytes", len(l))
			}
		}
		if strings.HasPrefix(l, "huge.txt") {
			t.Error("a file over 2 MiB was searched")
		}
	}
	if hits != maxHits || !strings.Contains(text, "[stopped at 200 hits") || !strings.Contains(text, "[files skipped because they are over 2 MiB: 1]") {
		t.Errorf("hits %d, notes in %q", hits, text[len(text)-120:])
	}
	text, _ = run(t, ws, "search", `{"pattern":"zzz"}`)
	if !strings.HasPrefix(text, "no match\n") {
		t.Errorf("no match = %q", text)
	}
	mustFail(t, ws, "search", `{"pattern":"a(b"}`, "not a valid RE2 regular expression")
	mustFail(t, ws, "search", `{"pattern":"(?=x)"}`, "not a valid RE2 regular expression")
	mustFail(t, ws, "search", `{"pattern":""}`, "needs a pattern")
	mustFail(t, ws, "search", `{"pattern":"x","glob":"[a"}`, "is not valid")
}

func TestListFilesCapAndSort(t *testing.T) {
	ws, dir := newWorkspace(t)
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".git", "config"), "")
	write(t, filepath.Join(dir, "a.txt"), "")
	write(t, filepath.Join(dir, "a", "b", "c.go"), "")
	text, _ := run(t, ws, "list_files", `{}`)
	if text != "a.txt\na/b/c.go\n" {
		t.Errorf("sorted list = %q", text)
	}
	for i := range maxListPaths + 5 {
		write(t, filepath.Join(dir, "a", fmt.Sprintf("f%05d.txt", i)), "")
	}
	text, _ = run(t, ws, "list_files", `{"glob":"a/*.txt"}`)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != maxListPaths+1 || lines[0] != "a/f00000.txt" || !strings.Contains(lines[len(lines)-1], "showing the first 2000 of 2005 paths") {
		t.Errorf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	text, _ = run(t, ws, "list_files", `{"glob":"*.md"}`)
	if text != "no file matches\n" {
		t.Errorf("no match = %q", text)
	}
}

func TestWalkCap(t *testing.T) {
	ws, dir := newWorkspace(t)
	old := maxVisited
	maxVisited = 10
	t.Cleanup(func() { maxVisited = old })
	for i := range 30 {
		write(t, filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), "")
	}
	text, _ := run(t, ws, "list_files", `{}`)
	if strings.Count(text, ".txt\n") != 10 || !strings.Contains(text, "the walk stopped after 10 entries") {
		t.Errorf("list = %q", text)
	}
}

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"", "a/b.go", true},
		{"*.go", "a/b.go", true},
		{"*.go", "b.go", true},
		{"**/*.go", "b.go", true},
		{"**/*.go", "a/b/c.go", true},
		{"internal/**/*_test.go", "internal/x/y/z_test.go", true},
		{"internal/**/*_test.go", "cmd/z_test.go", false},
		{"cmd/*", "cmd/a/b.go", false},
		{"cmd/*", "cmd/main.go", true},
		{"*.{ts,tsx}", "web/app.tsx", true},
		{"*.{ts,tsx}", "web/app.js", false},
		{"./a/*.go", "a/b.go", true},
	}
	for _, c := range cases {
		g, err := newGlob(c.pattern)
		if err != nil {
			t.Fatalf("%q: %v", c.pattern, err)
		}
		if got := g.match(c.path); got != c.want {
			t.Errorf("glob %q on %q = %v", c.pattern, c.path, got)
		}
	}
}

func TestCapResult(t *testing.T) {
	s := capResult(strings.Repeat("é", maxResultBytes))
	if len(s) > maxResultBytes || !strings.HasSuffix(s, "[the result was cut at 256 KiB]\n") {
		t.Errorf("capped to %d bytes", len(s))
	}
}
