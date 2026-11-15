// Package usage keeps an append-only log of what each planning job used, so the dashboard
// can show it. A record never holds a path, a repository name, a prompt, or model output.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/provider"
)

const FileName = "usage.jsonl"

type Record struct {
	At               time.Time        `json:"at"`
	Provider         string           `json:"provider"`
	Kind             string           `json:"kind"`
	Model            string           `json:"model"`
	Reasoning        string           `json:"reasoning,omitempty"`
	Command          string           `json:"command"`       // "plan"
	Ref              string           `json:"ref,omitempty"` // the plan ID
	Outcome          string           `json:"outcome"`       // "ok", "rejected", or "failed:<class>"
	Attempts         int              `json:"attempts"`
	Turns            int              `json:"turns"`
	InputTokens      int              `json:"inputTokens"`
	OutputTokens     int              `json:"outputTokens"`
	CacheReadTokens  int              `json:"cacheReadTokens"`
	CacheWriteTokens int              `json:"cacheWriteTokens"`
	CostUSD          float64          `json:"costUsd,omitempty"`
	DurationMs       int64            `json:"durationMs"`
	Limits           []provider.Limit `json:"limits,omitempty"`
}

// Append adds one line to <home>/usage.jsonl with a single write, so concurrent jobs do not
// interleave. It refuses a file that is a symbolic link.
func Append(home string, r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode a usage record: %w", err)
	}
	line = append(line, '\n')
	path := filepath.Join(home, FileName)
	if err := os.MkdirAll(home, atomicfile.DirMode); err != nil {
		return fmt.Errorf("create directory %s: %w", home, err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("open %s: %w", home, err)
	}
	defer root.Close()
	switch info, err := root.Lstat(FileName); {
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link, and Semar writes only a real file: replace the link with a file", path)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("check %s: %w", path, err)
	}
	f, err := root.OpenFile(FileName, os.O_WRONLY|os.O_APPEND|os.O_CREATE, atomicfile.FileMode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// Read returns the records at or after since, oldest first, in file order. A zero since
// returns all. A line that cannot be parsed is skipped, and a missing file is empty.
func Read(home string, since time.Time) ([]Record, error) {
	path := filepath.Join(home, FileName)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var out []Record
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var r Record
			if json.Unmarshal(line, &r) == nil && !r.At.Before(since) {
				out = append(out, r)
			}
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("read %s: %w", path, err)
		}
	}
}

// Row totals the records of one provider and model.
type Row struct {
	Provider, Kind, Model string
	Runs, Failed, Turns   int
	InputTokens           int
	OutputTokens          int
	CacheReadTokens       int
	CacheWriteTokens      int
	CostUSD               float64
}

// Summarize groups by provider, kind, and model, sorted by provider and then model. A run
// is failed when its outcome starts with "failed".
func Summarize(recs []Record) []Row {
	type key struct{ provider, kind, model string }
	rows := map[key]*Row{}
	for _, r := range recs {
		k := key{r.Provider, r.Kind, r.Model}
		row := rows[k]
		if row == nil {
			row = &Row{Provider: r.Provider, Kind: r.Kind, Model: r.Model}
			rows[k] = row
		}
		row.Runs++
		if strings.HasPrefix(r.Outcome, "failed") {
			row.Failed++
		}
		row.Turns += r.Turns
		row.InputTokens += r.InputTokens
		row.OutputTokens += r.OutputTokens
		row.CacheReadTokens += r.CacheReadTokens
		row.CacheWriteTokens += r.CacheWriteTokens
		row.CostUSD += r.CostUSD
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Kind < b.Kind
	})
	return out
}

// Limits are the subscription windows a provider reported in its newest record that had any.
type Limits struct {
	At      time.Time
	Windows []provider.Limit
}

// LatestLimits returns, per provider, the windows of the newest record that reported some.
func LatestLimits(recs []Record) map[string]Limits {
	out := map[string]Limits{}
	for _, r := range recs {
		if len(r.Limits) == 0 {
			continue
		}
		if cur, ok := out[r.Provider]; ok && r.At.Before(cur.At) {
			continue
		}
		out[r.Provider] = Limits{At: r.At, Windows: r.Limits}
	}
	return out
}
