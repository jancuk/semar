package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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

// recordPipelineUsage appends one record for each model that the criteria pipeline called, so the
// usage log says what each provider and model used.
func (o options) recordPipelineUsage(stderr io.Writer, insts []provider.Instance, out plan.Output, outcome, ref string) {
	if out.Pipeline == nil {
		return
	}
	for _, u := range out.Pipeline.Uses {
		if u.Calls == 0 {
			continue
		}
		i := slices.IndexFunc(insts, func(in provider.Instance) bool { return in.ID() == u.PlannerID })
		if i < 0 {
			continue
		}
		o.recordUsage(stderr, insts[i], u.Model, u.Reasoning, plan.Output{Result: u.Result, Attempts: u.Calls}, outcome, ref, u.Took)
	}
}

// useLine is the model use line of a plan, whichever pipeline made it.
func useLine(out plan.Output, setupOn bool) string {
	if out.Pipeline != nil {
		return pipelineUseLine(out)
	}
	return modelUseLine(out.Result, out.Attempts, setupOn)
}

// pipelineUseLine is the line of the criteria pipeline, for example
// "model use: 16 jobs (14 haiku, 2 sonnet), 3 corrections, 92k input, 21k output, $0.09". A job is a
// criterion, counted under the model that gave its accepted answer. A correction is a call that
// repaired an answer. A reused answer and a criterion that no model answered follow when there are
// any. A plan that stopped before it had answers says how many calls it made.
func pipelineUseLine(out plan.Output) string {
	st, r := out.Pipeline, out.Result
	if st.Calls == 0 && st.Reused == 0 {
		return ""
	}
	var parts []string
	if st.Criteria == 0 {
		parts = append(parts, plural(st.Calls, "call"))
	} else {
		jobs := plural(st.Criteria-st.Unanswered, "job")
		var by []string
		for _, m := range st.ByModel {
			if m.Answers > 0 {
				by = append(by, fmt.Sprintf("%d %s", m.Answers, terminalSafe(m.Model)))
			}
		}
		if len(by) > 0 {
			jobs += " (" + strings.Join(by, ", ") + ")"
		}
		parts = append(parts, jobs, plural(st.Corrections, "correction"))
	}
	input := tokenCount(r.InputTokens) + " input"
	if r.CacheReadTokens > 0 {
		input += " (" + tokenCount(r.CacheReadTokens) + " cached)"
	}
	parts = append(parts, input, tokenCount(r.OutputTokens)+" output")
	if r.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", r.CostUSD))
	}
	if st.Reused > 0 {
		parts = append(parts, fmt.Sprintf("%d reused", st.Reused))
	}
	if st.Unanswered > 0 {
		parts = append(parts, fmt.Sprintf("%d unanswered", st.Unanswered))
	}
	return "model use: " + strings.Join(parts, ", ")
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
