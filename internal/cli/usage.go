package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
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
