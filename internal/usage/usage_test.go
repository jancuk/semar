package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

var t0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func rec(i int, prov, model, outcome string) Record {
	return Record{At: t0.Add(time.Duration(i) * time.Minute), Provider: prov, Kind: "claude", Model: model,
		Command: "plan", Outcome: outcome, Attempts: 1, Turns: 2, InputTokens: 100, OutputTokens: 10, CostUSD: 0.5}
}

func TestAppendReadRoundTrip(t *testing.T) {
	home := filepath.Join(t.TempDir(), "semar")
	want := rec(1, "claude", "m1", "ok")
	want.Ref = "plan-1"
	want.CacheReadTokens, want.CacheWriteTokens, want.DurationMs = 7, 8, 1234
	want.Limits = []provider.Limit{{Window: "five_hour", Utilization: 0.4, ResetsAt: t0, Status: "allowed"}}
	if err := Append(home, want); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(home)
	fi, _ := os.Stat(filepath.Join(home, FileName))
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Errorf("modes: dir %v file %v", di.Mode().Perm(), fi.Mode().Perm())
	}
	got, err := Read(home, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("Read = %v, %v", got, err)
	}
	g, _ := json.Marshal(got[0])
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("got %s\nwant %s", g, w)
	}
}

func TestJSONKeysAreLowerCamel(t *testing.T) {
	r := rec(0, "p", "m", "ok")
	r.Ref, r.Limits = "x", []provider.Limit{{Window: "w"}}
	b, _ := json.Marshal(r)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for k := range m {
		if k == "" || k[0] < 'a' || k[0] > 'z' || strings.Contains(k, "_") {
			t.Errorf("key %q is not lower camel case", k)
		}
	}
	for _, k := range []string{"inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "costUsd", "durationMs"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %s in %s", k, b)
		}
	}
}

func TestReadMissingFileAndSince(t *testing.T) {
	home := t.TempDir()
	if got, err := Read(home, time.Time{}); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	for i := 0; i < 3; i++ {
		if err := Append(home, rec(i, "claude", "m", "ok")); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := Read(home, t0.Add(time.Minute))
	if len(got) != 2 {
		t.Errorf("since kept %d records, want 2", len(got))
	}
}

func TestReadSkipsBrokenLines(t *testing.T) {
	home := t.TempDir()
	_ = Append(home, rec(0, "a", "m", "ok"))
	f, _ := os.OpenFile(filepath.Join(home, FileName), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{not json\n\n[1,2]\n")
	_ = f.Close()
	_ = Append(home, rec(1, "b", "m", "ok"))
	// A torn final line without a newline.
	f, _ = os.OpenFile(filepath.Join(home, FileName), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"at":"2026`)
	_ = f.Close()
	got, err := Read(home, time.Time{})
	if err != nil || len(got) != 2 || got[0].Provider != "a" || got[1].Provider != "b" {
		t.Fatalf("Read = %+v, %v", got, err)
	}
}

func TestConcurrentAppends(t *testing.T) {
	home := t.TempDir()
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rec(i, "claude", "m", "ok")
			r.Ref = strings.Repeat("x", 3000) // long enough to tear a split write
			if err := Append(home, r); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, _ := os.ReadFile(filepath.Join(home, FileName))
	if lines := strings.Count(string(data), "\n"); lines != n {
		t.Errorf("%d lines, want %d", lines, n)
	}
	got, _ := Read(home, time.Time{})
	if len(got) != n {
		t.Errorf("read %d valid records, want %d", len(got), n)
	}
}

func TestAppendRefusesSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "other.jsonl")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, FileName)); err != nil {
		t.Skip("no symlinks:", err)
	}
	err := Append(home, rec(0, "a", "m", "ok"))
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Append = %v", err)
	}
	if b, _ := os.ReadFile(target); len(b) != 0 {
		t.Error("the link target was written")
	}
}

func TestSummarize(t *testing.T) {
	recs := []Record{
		rec(0, "b", "m2", "ok"),
		rec(1, "a", "m2", "failed:usage_limit"),
		rec(2, "a", "m1", "ok"),
		rec(3, "a", "m1", "rejected"),
		rec(4, "a", "m1", "failed:timeout"),
	}
	rows := Summarize(recs)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	order := [][2]string{{"a", "m1"}, {"a", "m2"}, {"b", "m2"}}
	for i, o := range order {
		if rows[i].Provider != o[0] || rows[i].Model != o[1] {
			t.Errorf("row %d = %s/%s, want %s/%s", i, rows[i].Provider, rows[i].Model, o[0], o[1])
		}
	}
	r := rows[0]
	if r.Runs != 3 || r.Failed != 1 || r.Turns != 6 || r.InputTokens != 300 || r.OutputTokens != 30 || r.CostUSD != 1.5 {
		t.Errorf("a/m1 = %+v", r)
	}
	if len(Summarize(nil)) != 0 {
		t.Error("empty input gave rows")
	}
}

func TestLatestLimits(t *testing.T) {
	lim := func(u float64) []provider.Limit { return []provider.Limit{{Window: "five_hour", Utilization: u}} }
	a1, a2, b1 := rec(1, "a", "m", "ok"), rec(5, "a", "m", "ok"), rec(2, "b", "m", "ok")
	a1.Limits, a2.Limits, b1.Limits = lim(0.1), lim(0.9), lim(0.3)
	api := rec(9, "a", "m", "ok") // newer, but reports none: it must not hide the last report
	got := LatestLimits([]Record{a2, a1, b1, api})
	if len(got) != 2 || got["a"].Windows[0].Utilization != 0.9 || !got["a"].At.Equal(a2.At) || got["b"].Windows[0].Utilization != 0.3 {
		t.Errorf("LatestLimits = %+v", got)
	}
}
