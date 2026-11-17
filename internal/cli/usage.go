package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/usage"
)

// usageOutcome is ok for no error, rejected for a draft that failed every attempt, and
// failed:<class> for anything else.
func usageOutcome(ctx context.Context, err error) string {
	var rejected *plan.Rejected
	var perr *provider.Error
	switch {
	case err == nil:
		return "ok"
	case ctx.Err() != nil:
		return "failed:interrupted"
	case errors.As(err, &rejected):
		return "rejected"
	case errors.As(err, &perr):
		return "failed:" + string(perr.Class)
	}
	return "failed:" + string(provider.Unknown)
}

// recordUsage appends one record to the person's usage log. A failure is one notice, because
// the plan itself has succeeded or failed already and the log is not worth a second failure.
func (o options) recordUsage(stderr io.Writer, inst provider.Instance, model, reasoning string, out plan.Output, outcome, ref string, took time.Duration) {
	r := out.Result
	if r.Model != "" {
		model = r.Model
	}
	if r.Reasoning != "" {
		reasoning = r.Reasoning
	}
	rec := usage.Record{
		At: o.Clock(), Provider: inst.ID(), Kind: inst.Kind(), Model: model, Reasoning: reasoning, Command: "plan", Ref: ref,
		Outcome: outcome, Attempts: out.Attempts, Turns: r.Turns,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		CacheReadTokens: r.CacheReadTokens, CacheWriteTokens: r.CacheWriteTokens,
		CostUSD: r.CostUSD, DurationMs: took.Milliseconds(), Limits: r.Limits,
	}
	home, err := o.home()
	if err == nil {
		err = usage.Append(home, rec)
	}
	if err != nil {
		fmt.Fprintf(stderr, "semar: the usage of this job was not recorded: %v\n", err)
	}
}

// modelUseLine is the one line that says what a plan used, summed over its attempts, for example
// "model use: 2 attempts, 21 turns, 640k input (590k cached), 18k output, $1.10". The cached part
// and the cost appear only when the provider reported them. A job that never ran gives "". With
// the setup on, a second line says that it adds tokens to every turn.
func modelUseLine(r provider.Result, attempts int, setupOn bool) string {
	if attempts == 0 && r.Turns == 0 && r.InputTokens == 0 && r.OutputTokens == 0 {
		return ""
	}
	parts := []string{plural(attempts, "attempt"), plural(r.Turns, "turn"), tokenCount(r.InputTokens) + " input"}
	if r.CacheReadTokens > 0 {
		parts[2] += " (" + tokenCount(r.CacheReadTokens) + " cached)"
	}
	parts = append(parts, tokenCount(r.OutputTokens)+" output")
	if r.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", r.CostUSD))
	}
	line := "model use: " + strings.Join(parts, ", ")
	if setupOn {
		line += "\nyour MCP servers and skills were loaded, and they add tokens to every turn; plan without --with-setup to leave them out"
	}
	return line
}

// tokenCount writes a number of tokens as 850, 6.1k, 640k, or 2.7M.
func tokenCount(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 9_950:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 999_500:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}
