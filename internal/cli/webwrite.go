package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
)

// webWriteRequest is what a driven recording needs to become a web test: the names of its files, the
// claims of its checks, and how to replay it. The model of semar plan and the agent of semar mcp send
// the same request, so a test is written, replayed, and removed the same way for both.
type webWriteRequest struct {
	ID          domain.ScenarioID
	Ticket      string
	Subject     string // the subject of the screens file; the ticket when empty
	App         string
	Service     string
	BaseURL     string
	AuthProfile string
	Given       []domain.Given
	Vars        map[string]string
	SecretEnv   string
	Claims      []record.Claim
	// Title and Intent replace the ones that the recording made when Retitle is set, empty or not.
	Title, Intent string
	Retitle       bool
	// ByModel and Author mark the checks and the script as written by an agent.
	ByModel bool
	Author  *domain.ProviderRef
	// Replace lists the files of a scenario of the same ID that may be replaced.
	Replace []string
	// Verify replays the written test once and removes it when the replay does not end as recorded.
	Verify bool
	Builds []string
}

// webWriteKind says why nothing was kept, for a caller that words it for its own reader.
type webWriteKind string

const (
	webWriteNoCheck webWriteKind = "no_check"
	webWriteSecret  webWriteKind = "secret"
	webWriteReplay  webWriteKind = "replay"
)

// webWriteResult is what writeWebTest gives. Manual is not empty when nothing was kept, and then the
// repository is as it was. Replay is the outcome of the replay, or "" when it was not asked for.
type webWriteResult struct {
	Draft   record.Draft
	Written record.Written
	Replay  string
	Remove  func() error
	Manual  string
	Kind    webWriteKind
}

// writeWebTest builds the test from what the browser heard, writes it as semar record does, and replays
// it once. The replay must end as the recording predicts: passed when every check met its claim, failed
// when one did not. A test that ends otherwise is removed, because a wrong test costs more than a manual
// check. An error is a failure of the process, such as a cancel, and a Manual is a test that was not kept.
func writeWebTest(ctx context.Context, o options, dir, envName string, req webWriteRequest, events []record.Event) (webWriteResult, error) {
	manualRes := func(kind webWriteKind, format string, args ...any) (webWriteResult, error) {
		return webWriteResult{Manual: fmt.Sprintf(format, args...), Kind: kind}, nil
	}
	opts := record.Options{
		ID: req.ID, Ticket: req.Ticket, App: req.App, Service: req.Service, BaseURL: req.BaseURL,
		AuthProfile: req.AuthProfile, Given: req.Given, Values: record.NewValues(req.Vars), SecretEnv: req.SecretEnv,
		Date: o.Clock(), Claims: req.Claims, ByModel: req.ByModel, Author: req.Author,
	}
	var err error
	if opts.Existing, err = screens.Load(dir, req.App); err != nil && !errors.Is(err, screens.ErrNotFound) {
		return webWriteResult{}, err
	}
	draft, err := record.Build(events, opts)
	switch {
	case errors.Is(err, record.ErrNoCheck):
		return manualRes(webWriteNoCheck, "the browser heard no check, so nothing proves the criterion")
	case errors.Is(err, record.ErrSecretName):
		return manualRes(webWriteSecret, "the model typed into a password field, which a test cannot keep")
	case err != nil:
		return manualRes("", "the recording could not be made into a test: %s", terminalSafe(oneLine(err.Error())))
	}
	if req.Retitle {
		draft.Scenario.Title = oneLine(req.Title)
		draft.Scenario.Intent = oneLine(req.Intent)
	}
	draft.Script.ExpectHash = domain.ExpectHash(draft.Scenario)
	draft.Script.ShapeHash = domain.ShapeHash(draft.Scenario)
	if err := checkDraft(draft); err != nil {
		return manualRes("", "%s", strings.TrimPrefix(err.Error(), "nothing was written: "))
	}
	subject := req.Subject
	if subject == "" {
		subject = req.Ticket
	}
	written, err := persistDraft(dir, draft, req.App, subject, req.Replace)
	if err != nil {
		return webWriteResult{}, err
	}
	remove := func() error {
		err := written.Undo()
		store, serr := evidence.Open(semarDir(dir))
		if serr == nil {
			_, serr = store.RemoveScripts(req.ID)
		}
		return errors.Join(err, serr)
	}
	res := webWriteResult{Draft: draft, Written: written, Remove: remove}
	if !req.Verify {
		return res, nil
	}
	r := replayScenario(ctx, o, dir, envName, string(req.ID), req.Builds)
	if ctx.Err() != nil {
		_ = remove()
		return webWriteResult{}, ctx.Err()
	}
	want := domain.OutcomePassed
	if len(draft.FailsNow) > 0 {
		want = domain.OutcomeFailed
	}
	if r.result == nil || r.result.Outcome != want {
		why := "it did not finish"
		if r.result != nil {
			why = "it ended " + string(r.result.Outcome)
			if len(r.result.Reasons) > 0 {
				why += ": " + terminalSafe(oneLine(r.result.Reasons[0].Text))
			}
		} else if r.err != nil {
			why = "it could not run: " + terminalSafe(oneLine(r.err.Error()))
		}
		_ = remove()
		return manualRes(webWriteReplay, "the replay of the written test did not end as the recording predicts, %s was expected and %s, so the test was removed", want, why)
	}
	res.Replay = string(r.result.Outcome)
	return res, nil
}
