package dashboard

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/usage"
)

const latestRuns = 50

type usageBody struct {
	Days   int                    `json:"days"`
	Since  time.Time              `json:"since"`
	Rows   []usageRow             `json:"rows"`
	Limits map[string]usageLimits `json:"limits"`
	Recent []usage.Record         `json:"recent"`
}

type usageRow struct {
	Provider         string  `json:"provider"`
	Kind             string  `json:"kind"`
	Model            string  `json:"model"`
	Runs             int     `json:"runs"`
	Failed           int     `json:"failed"`
	Turns            int     `json:"turns"`
	InputTokens      int     `json:"inputTokens"`
	OutputTokens     int     `json:"outputTokens"`
	CacheReadTokens  int     `json:"cacheReadTokens"`
	CacheWriteTokens int     `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd"`
}

type usageLimits struct {
	At      time.Time        `json:"at"`
	Windows []provider.Limit `json:"windows"`
}

// getUsage counts days in local calendar days, so that one day means today.
func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			fail(w, http.StatusBadRequest, "days must be a whole number from 1 to 365.")
			return
		}
		days = n
	}
	now := s.d.Now()
	y, m, d := now.Date()
	since := time.Date(y, m, d-(days-1), 0, 0, 0, 0, now.Location())
	recs, err := usage.Read(s.d.Home, since)
	if err != nil {
		fail(w, http.StatusInternalServerError, "The usage file could not be read: "+scrub(err.Error()))
		return
	}
	body := usageBody{Days: days, Since: since, Rows: []usageRow{}, Limits: map[string]usageLimits{}, Recent: []usage.Record{}}
	for _, row := range usage.Summarize(recs) {
		body.Rows = append(body.Rows, usageRow(row))
	}
	for id, l := range usage.LatestLimits(recs) {
		body.Limits[id] = usageLimits{At: l.At, Windows: l.Windows}
	}
	recent := slices.Clone(recs)
	slices.SortStableFunc(recent, func(a, b usage.Record) int { return b.At.Compare(a.At) })
	if len(recent) > latestRuns {
		recent = recent[:latestRuns]
	}
	body.Recent = append(body.Recent, recent...)
	reply(w, http.StatusOK, body)
}

// plainDuration says a duration in words a person reads, such as "5 minutes".
func plainDuration(d time.Duration) string {
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		if d == time.Minute {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", d/time.Minute)
	case d >= time.Second && d%time.Second == 0:
		if d == time.Second {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", d/time.Second)
	default:
		return d.String()
	}
}
