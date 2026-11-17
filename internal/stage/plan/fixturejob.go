package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/prompt"
)

// MaxFixtureDrafts is the most fixtures that one plan drafts. A fixture is one more job for each
// attempt, and a plan that needs more has criteria that a person should look at first.
const MaxFixtureDrafts = 5

// The limits of a fixture file (rule F8).
const (
	maxCreateSteps  = 16
	maxCleanupSteps = 8
)

// FixturePacket is everything that a job needs to draft one fixture: the fixture that the criteria
// named, and the operations of the criteria that need it. Code builds it without a model.
type FixturePacket struct {
	Need       FixtureNeed
	Operations []PacketOperation
	Account    string
	// Accounts are the auth profiles that a step may act as. With fewer than two there is no choice,
	// and the schema and the prompt leave the question out.
	Accounts []Account
	Version  string
	// shim is the packet whose checks of an operation's parameters, body, and response fields a
	// fixture step goes through, as the step of a scenario does.
	shim Packet
}

// NewFixturePacket takes the operations that the criteria which need the fixture could call.
func NewFixturePacket(in Input, need FixtureNeed, ops []PacketOperation, version string) FixturePacket {
	return FixturePacket{
		Need: need, Operations: ops, Account: in.AuthProfile, Accounts: in.Accounts, Version: version,
		shim: Packet{Operations: ops, Account: in.AuthProfile},
	}
}

// FixtureAnswer is what a job returns for a fixture. A step names an operation of its packet, and
// never writes a method, a path, or a header: code supplies them.
type FixtureAnswer struct {
	Create  []FixtureAnswerStep `json:"create"`
	Outputs []string            `json:"outputs"`
	Cleanup []FixtureAnswerStep `json:"cleanup"`
}

type FixtureAnswerStep struct {
	As        string         `json:"as"`
	Operation string         `json:"operation"`
	Path      []NamedValue   `json:"path"`
	Query     []NamedValue   `json:"query"`
	Body      []BodyField    `json:"body"`
	Captures  []CaptureDraft `json:"captures"`
}

// DecodeFixtureAnswer reads the output of a fixture job strictly, as DecodeAnswer does.
func DecodeFixtureAnswer(data []byte) (FixtureAnswer, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var e struct {
		Fixture *FixtureAnswer `json:"fixture"`
	}
	if err := dec.Decode(&e); err != nil {
		return FixtureAnswer{}, err
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return FixtureAnswer{}, errors.New("data follows the answer object")
	}
	if e.Fixture == nil {
		return FixtureAnswer{}, errors.New("the answer has no fixture object")
	}
	return *e.Fixture, nil
}

// Schema is the JSON Schema of an answer to this packet. It is closed like the schema of a
// criterion: a step chooses its operation from the packet, and its parameters, fields, and captures
// from what that operation declares.
func (fp FixturePacket) Schema() json.RawMessage {
	create := array(fp.stepSchema(), "The requests that make the data, in order.")
	cleanup := array(fp.stepSchema(), "The requests that remove what create made, in order. Empty when no operation removes it.")
	root := object(map[string]any{"fixture": object(map[string]any{
		"create":  create,
		"outputs": array(text("The name of a capture that scenarios read."), "The values that scenarios read from the fixture."),
		"cleanup": cleanup,
	})})
	data, err := json.Marshal(root)
	if err != nil {
		panic(fmt.Sprintf("encode the fixture schema: %v", err)) // the schema holds strings and maps
	}
	return data
}

func (fp FixturePacket) choosesAccount() bool { return len(fp.Accounts) >= 2 }

func (fp FixturePacket) stepSchema() any {
	var branches []any
	for _, op := range fp.Operations {
		props := map[string]any{
			"operation": enum("string", op.Key),
			"path":      array(pathItems(op), "The path parameters. Each is {{name}} of an earlier capture."),
			"query":     array(queryItems(op), "The query parameters."),
			"body":      array(bodyItems(op), "The fields of the JSON body."),
			"captures":  array(captureItem(op), "Values of this response that a later request uses as {{name}}."),
		}
		if fp.choosesAccount() {
			props["as"] = Packet{Accounts: fp.Accounts, Account: fp.Account}.accountSchema()
		}
		branches = append(branches, object(props))
	}
	if len(branches) == 0 {
		return object(map[string]any{"operation": text("No operation exists.")})
	}
	return anyOf(branches)
}

// Prompt is the text of a fixture job. With a previous output and problems it is a correction.
func (fp FixturePacket) Prompt(previous []byte, problems []Problem) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\nstate: %s\noutputs: %s", fp.Need.ID, oneLine(fp.Need.State, maxProposalDesc), strings.Join(fp.Need.Outputs, ", "))
	d := prompt.FixtureData{
		Fixture:    fenced("text", b.String()),
		Operations: operationList(fp.Operations),
		Account:    fp.Account,
	}
	if fp.choosesAccount() {
		d.Accounts = accountList(fp.Accounts)
	}
	if len(problems) > 0 {
		d.Correction = true
		d.Problems = problemList(problems)
		if len(previous) > 0 {
			d.Previous = fenced("json", string(previous))
		} else {
			d.Previous = "The output was empty."
		}
	}
	return prompt.Fixture(d)
}

// Convert checks an answer against the packet and turns it into a fixture, and then runs the rules
// of a fixture file over it. The fixture comes back only when no problem was found.
func (fp FixturePacket) Convert(a FixtureAnswer, in Input) (domain.Fixture, []Problem) {
	var ps problems
	if n := len(a.Create); n < 1 || n > maxCreateSteps {
		ps.add("create", "a fixture has 1 to %d create steps, and this one has %d", maxCreateSteps, n)
	}
	if len(a.Cleanup) > maxCleanupSteps {
		ps.add("cleanup", "a fixture has at most %d cleanup steps, and this one has %d", maxCleanupSteps, len(a.Cleanup))
	}
	fx := domain.Fixture{
		APIVersion: "semar/v1", Kind: "Fixture", ID: domain.FixtureID(fp.Need.ID),
		Description: fp.Need.State, Version: "1.0.0", Owner: "semar", Outputs: slices.Clone(a.Outputs),
	}
	fx.Create = fp.steps(a.Create, "create", in, &ps)
	fx.Cleanup = fp.steps(a.Cleanup, "cleanup", in, &ps)
	fp.checkAccounts(fx, &ps)
	if len(ps.list) > 0 {
		return domain.Fixture{}, ps.list
	}
	var opts []fixture.Option
	if len(in.Accounts) > 0 {
		names := make([]string, len(in.Accounts))
		for i, a := range in.Accounts {
			names[i] = a.Name
		}
		opts = append(opts, fixture.Accounts(names...))
	}
	for _, p := range fixture.Validate(fx, in.Services, opts...) {
		ps.add(p.Path, "%s", p.Message)
	}
	if len(ps.list) > 0 {
		return domain.Fixture{}, ps.list
	}
	return fx, nil
}

// checkAccounts refuses an answer whose description names an account that no step acts as: the
// fixture would make the data as the default account, and a scenario that needs it to belong to
// another account would fail on a correct build.
func (fp FixturePacket) checkAccounts(fx domain.Fixture, ps *problems) {
	acting := fx.Accounts()
	for _, name := range fp.accountsNamed() {
		if !slices.Contains(acting, name) {
			ps.add("create", "the description names the account %s, and no step acts as %s: set as on each step that %s sends", name, name, name)
		}
	}
}

// accountsNamed are the accounts other than the default that the description of the fixture names as
// a word, in any case. A hyphen, a digit, and an underscore belong to the word, so support does not
// name support-agent.
func (fp FixturePacket) accountsNamed() []string {
	var out []string
	for _, a := range fp.Accounts {
		if a.Name == fp.Account || a.Name == "" {
			continue
		}
		word := regexp.MustCompile(`(?i)(?:^|[^a-z0-9_-])` + regexp.QuoteMeta(a.Name) + `(?:$|[^a-z0-9_-])`)
		if word.MatchString(fp.Need.State) {
			out = append(out, a.Name)
		}
	}
	slices.Sort(out)
	return out
}

func (fp FixturePacket) steps(list []FixtureAnswerStep, phase string, in Input, ps *problems) []domain.FixtureStep {
	var out []domain.FixtureStep
	for i, st := range list {
		path := fmt.Sprintf("%s[%d]", phase, i)
		sd, _ := fp.shim.convertStep(AnswerStep{
			ID: fmt.Sprintf("%s-%d", phase, i+1), Setup: true, Action: phase, Operation: st.Operation,
			Path: st.Path, Query: st.Query, Body: st.Body, Captures: st.Captures,
		}, path, in, ps)
		op, ok := fp.shim.operation(st.Operation)
		if !ok {
			continue
		}
		service := op.Service
		if !slices.Contains(in.Services, service) && len(in.Services) > 0 {
			service = in.Services[0]
		}
		step := domain.FixtureStep{Request: domain.APIRequest{
			Service: service, Method: sd.Method, Path: sd.Path, Query: sd.Query, JSON: plainJSON(sd.JSON),
		}}
		for _, c := range sd.Captures {
			step.Captures = append(step.Captures, domain.Capture{Name: c.Name, JSONPath: c.JSONPath})
		}
		if st.As != fp.Account {
			step.As = st.As
		}
		out = append(out, step)
	}
	return out
}

// plainJSON turns the numbers of a body, which the answer keeps as text, into numbers that a YAML
// file writes as numbers.
func plainJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if r, ok := new(big.Rat).SetString(x.String()); ok && r.IsInt() && r.Num().IsInt64() {
			return r.Num().Int64()
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = plainJSON(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plainJSON(e)
		}
		return out
	}
	return v
}

// FixtureTrialer runs a drafted fixture once against a test environment that allows writes: its create
// steps, and then its cleanup. A nil FixtureTrialer turns the run off.
type FixtureTrialer interface {
	TryFixture(ctx context.Context, fx domain.Fixture) (FixtureTrial, error)
}

// FixtureTrial is what one run of a fixture found. It holds statuses and names, never a value of a
// response.
type FixtureTrial struct {
	Ran       bool     // the fixture was sent
	Skipped   string   // why nothing was sent, in one line
	Problems  []string // what went wrong, in plain words, each starting with "trial:"
	Requests  int      // the requests that were sent, cleanup included
	CleanedUp bool     // the cleanup ran and finished
	Left      []string // what may still exist after the cleanup
}

// FixtureDraft is how one needed fixture fared.
type FixtureDraft struct {
	Need         FixtureNeed
	Drafted      bool
	Fixture      domain.Fixture
	Rung         int // the rung that answered, or the last that tried
	Corrections  int
	Trial        FixtureTrial
	Problems     []Problem // the problems of the last answer, when none was accepted
	Returned     []Problem // every problem that went back to a model
	Reason       string    // why it was not drafted, when no model was asked
	LimitReached bool      // the cost limit stopped the job
	Output       []byte
}

// capNeeds keeps the first MaxFixtureDrafts needs, and returns the rest.
func capNeeds(needs []FixtureNeed) (kept, left []FixtureNeed) {
	if len(needs) <= MaxFixtureDrafts {
		return needs, nil
	}
	return needs[:MaxFixtureDrafts], needs[MaxFixtureDrafts:]
}

// maxFixtureOperations bounds the operations of a fixture job, which its schema lists in full.
const maxFixtureOperations = 40

// fixtureOperations are the operations that a fixture job may call: the ones that the criteria which
// need it could call, and then the rest of the API documents, because the state that a criterion
// needs often takes an operation that its own packet left out. Only a method that the environment
// allows is offered.
func fixtureOperations(in Input, first []PacketOperation) []PacketOperation {
	ops := slices.Clone(first)
	for _, s := range in.Knowledge {
		if !s.Usable() || s.Kind != config.KnowledgeOpenAPI || s.API == nil {
			continue
		}
		for _, op := range s.API.Operations {
			po := packetOperation(s, op)
			switch {
			case len(ops) >= maxFixtureOperations:
				return ops
			case len(in.Methods) > 0 && !slices.Contains(in.Methods, po.Method):
			case slices.ContainsFunc(ops, func(x PacketOperation) bool { return x.Key == po.Key }):
			default:
				ops = append(ops, po)
			}
		}
	}
	return ops
}

// needOperations are the operations of the criteria that need a fixture, once each, in the order of
// the criteria.
func needOperations(n FixtureNeed, outcomes []CriterionOutcome) []PacketOperation {
	var ops []PacketOperation
	for _, o := range outcomes {
		if o.Result.Need == nil || o.Result.Need.ID != n.ID {
			continue
		}
		for _, op := range o.Packet.Operations {
			if !slices.ContainsFunc(ops, func(x PacketOperation) bool { return x.Key == op.Key }) {
				ops = append(ops, op)
			}
		}
	}
	return ops
}

// draftFixtures asks the ladder for each needed fixture, at most MaxFixtureDrafts, MaxParallelJobs at
// a time. A fixture that no model could write is a draft that says why.
func (rs *runState) draftFixtures(ctx context.Context, needs []FixtureNeed, outcomes []CriterionOutcome) ([]FixtureDraft, error) {
	kept, left := capNeeds(needs)
	drafts := make([]FixtureDraft, len(kept), len(needs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxParallelJobs)
	for i, n := range kept {
		g.Go(func() error {
			fp := NewFixturePacket(rs.in, n, fixtureOperations(rs.in, needOperations(n, outcomes)), prompt.FixtureVersion())
			d, err := rs.draftFixture(gctx, fp)
			if err != nil {
				return fmt.Errorf("fixture %s: %w", n.ID, err)
			}
			drafts[i] = d
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for _, n := range left {
		drafts = append(drafts, FixtureDraft{Need: n, Reason: fmt.Sprintf("a plan drafts at most %d fixtures, and this was the %dth that criteria needed", MaxFixtureDrafts, len(kept)+1)})
	}
	return drafts, nil
}

// draftFixture is one job, a correction of its answer, and the same again at the next rung. The run
// of the fixture is a check like any other: its problems go back to the model in the same bounds.
func (rs *runState) draftFixture(ctx context.Context, fp FixturePacket) (FixtureDraft, error) {
	d := FixtureDraft{Need: fp.Need}
	try := func(rung int, previous []byte, ps []Problem) (accepted bool, err error) {
		if rs.overLimit() {
			d.Reason, d.LimitReached = notPlanned(rs.in), true
			return false, errLimit
		}
		raw, soft, err := rs.callFixture(ctx, rung, fp, previous, ps)
		if err != nil {
			return false, err
		}
		d.Rung, d.Output = rung, raw
		fx, tr, found, err := rs.checkFixture(ctx, fp, raw, soft)
		if err != nil {
			return false, err
		}
		if len(found) > 0 {
			d.Problems = found
			return false, nil
		}
		d.Drafted, d.Fixture, d.Trial, d.Problems = true, fx, tr, nil
		return true, nil
	}
	for rung := range rs.in.Ladder {
		if ok, err := try(rung, nil, nil); err != nil || ok {
			return d, stopped(err)
		}
		d.Corrections++
		d.Returned = append(d.Returned, d.Problems...)
		if ok, err := try(rung, d.Output, d.Problems); err != nil || ok {
			return d, stopped(err)
		}
	}
	return d, nil
}

// errLimit ends a fixture job that the cost limit refused. It is not an error of the run.
var errLimit = errors.New("the cost limit was reached")

func stopped(err error) error {
	if errors.Is(err, errLimit) {
		return nil
	}
	return err
}

func (rs *runState) callFixture(ctx context.Context, rung int, fp FixturePacket, previous []byte, problems []Problem) ([]byte, string, error) {
	text, err := fp.Prompt(previous, problems)
	if err != nil {
		return nil, "", err
	}
	return rs.run(ctx, rung, text, fp.Schema(), len(problems) > 0)
}

// checkFixture decodes an output, checks it, and runs the fixture once when the person allows it. A
// trial that cannot run is no problem of the answer.
func (rs *runState) checkFixture(ctx context.Context, fp FixturePacket, raw []byte, soft string) (domain.Fixture, FixtureTrial, []Problem, error) {
	if soft != "" {
		return domain.Fixture{}, FixtureTrial{}, []Problem{{Message: "the job returned no answer in the schema: " + soft}}, nil
	}
	a, err := DecodeFixtureAnswer(raw)
	if err != nil {
		return domain.Fixture{}, FixtureTrial{}, []Problem{{Message: "the output is not a fixture answer in the schema: " + err.Error()}}, nil
	}
	fx, ps := fp.Convert(a, rs.in)
	if len(ps) > 0 {
		return domain.Fixture{}, FixtureTrial{}, ps, nil
	}
	if rs.in.FixtureTrial == nil {
		return fx, FixtureTrial{Skipped: rs.in.fixtureTrialOff()}, nil, nil
	}
	tr, err := rs.in.FixtureTrial.TryFixture(ctx, fx)
	if err != nil {
		if ctx.Err() != nil {
			return domain.Fixture{}, FixtureTrial{}, nil, fmt.Errorf("fixture trial: %w", err)
		}
		tr = FixtureTrial{Skipped: "the run stopped: " + err.Error()}
	}
	for _, msg := range tr.Problems {
		ps = append(ps, Problem{Message: msg})
	}
	if len(ps) > 0 {
		return domain.Fixture{}, tr, ps, nil
	}
	return fx, tr, nil, nil
}

func (in Input) fixtureTrialOff() string {
	if in.TrialOff != "" {
		return in.TrialOff
	}
	return "no trial of fixtures was set up"
}

// replan plans each criterion that needed a drafted fixture once more, with the fixture in its
// packet, at the first rung. A criterion whose fixture was not drafted stays a gap that says why.
func (rs *runState) replan(ctx context.Context, outcomes []CriterionOutcome, drafts []FixtureDraft) error {
	drafted := map[string]bool{}
	reason := map[string]string{}
	for _, d := range drafts {
		switch {
		case d.Drafted:
			drafted[d.Need.ID] = true
		case d.Reason != "":
			reason[d.Need.ID] = d.Reason
		default:
			reason[d.Need.ID] = firstProblem(d.Problems)
		}
	}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxParallelJobs)
	for i, o := range outcomes {
		n := o.Result.Need
		if !o.Answered || n == nil {
			continue
		}
		if !drafted[n.ID] {
			if o.Result.NotDrafted != nil {
				nd := *o.Result.NotDrafted
				nd.Needs += ". The fixture was not drafted: " + reason[n.ID]
				outcomes[i].Result.NotDrafted = &nd
			}
			continue
		}
		g.Go(func() error {
			p := o.Packet
			p.Fixtures = slices.Clone(rs.in.Fixtures)
			again, err := rs.answer(gctx, p)
			if err != nil {
				return fmt.Errorf("criterion %s: %w", p.Cite, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if again.NotPlanned {
				if o.Result.NotDrafted != nil {
					nd := *o.Result.NotDrafted
					nd.Needs += ". The fixture was drafted, and the criterion was not planned again: " + strings.TrimPrefix(notPlanned(rs.in), "not planned: ")
					outcomes[i].Result.NotDrafted = &nd
				}
				return nil
			}
			again.Corrections += o.Corrections
			outcomes[i] = again
			return nil
		})
	}
	return g.Wait()
}

func firstProblem(ps []Problem) string {
	if len(ps) == 0 {
		return "no model gave an answer that passed the checks"
	}
	msg := oneLine(ps[0].Message, maxProblemRunes)
	if ps[0].Path != "" {
		msg = ps[0].Path + ": " + msg
	}
	return msg
}

// entryOf is what the catalogue shows of a drafted fixture to the criteria that are planned again.
func entryOf(fx domain.Fixture) fixture.Entry {
	e := fixture.Entry{ID: fx.ID, Description: fx.Description, Outputs: slices.Clone(fx.Outputs)}
	for _, name := range slices.Sorted(maps.Keys(fx.Params)) {
		e.Params = append(e.Params, fixture.ParamEntry{Name: name, Required: fx.Params[name].Default == nil})
	}
	return e
}
