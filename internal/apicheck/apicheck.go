// Package apicheck judges an API response against the API document and against a time limit. It
// reads only what the engine already has, and returns a finding that holds paths and types, never
// a value of the response.
package apicheck

import (
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

// The values of environments.<name>.checks.schema and of checks.responseTime.on.
const (
	Fail = "fail"
	Warn = "warn"
	Off  = "off"
)

// Source is an API document and the service it describes. An empty Service means every service.
type Source struct {
	Service string
	Doc     openapi.Document
}

type Options struct {
	Env     string
	Schema  string // Fail, Warn, or Off; empty means Fail
	Limit   time.Duration
	LimitOn string // Warn or Fail; empty means Warn
	Docs    []Source
}

type Checker struct {
	o Options
}

// New returns nil when there is nothing to check, and a nil Checker finds nothing.
func New(o Options) *Checker {
	if o.Schema == "" {
		o.Schema = Fail
	}
	if o.LimitOn == "" {
		o.LimitOn = Warn
	}
	if (o.Schema == Off || len(o.Docs) == 0) && o.Limit <= 0 {
		return nil
	}
	return &Checker{o: o}
}

// Response is what the engine knows of one answer.
type Response struct {
	Service, Method string
	// Path is the path of the call, and FullPath the path of the URL with the base path of the service.
	Path, FullPath string
	Status         int
	Body           []byte
	Truncated      bool
	DurationMS     int64
}

// Check returns nil when the response has nothing to report.
func (c *Checker) Check(r Response) *domain.DocCheck {
	if c == nil {
		return nil
	}
	out := domain.DocCheck{Env: c.o.Env}
	if c.o.Schema != Off {
		if doc, op, found := c.operation(r); found {
			out.Operation = op.Method + " " + op.Path
			c.judge(&out, doc, op, r)
		}
	}
	if c.o.Limit > 0 && r.DurationMS > c.o.Limit.Milliseconds() {
		out.LimitMS, out.TookMS = c.o.Limit.Milliseconds(), r.DurationMS
		out.SlowFail = c.o.LimitOn == Fail
	}
	if len(out.Misfits) == 0 && !out.Undeclared && out.LimitMS == 0 {
		return nil
	}
	return &out
}

func (c *Checker) operation(r Response) (openapi.Document, openapi.Operation, bool) {
	for _, s := range c.o.Docs {
		if s.Service != "" && s.Service != r.Service {
			continue
		}
		m := s.Doc.MatchPath(r.Method, r.Path, r.FullPath)
		if m.Found {
			return s.Doc, m.Operation, true
		}
	}
	return openapi.Document{}, openapi.Operation{}, false
}

// bodyless are the statuses and methods that carry no body to check.
func bodyless(method string, status int) bool {
	return method == "HEAD" || status == 204 || status == 304 || status < 200
}

func (c *Checker) judge(out *domain.DocCheck, doc openapi.Document, op openapi.Operation, r Response) {
	if !op.Declares(r.Status) {
		out.Undeclared, out.Status = true, r.Status
		return
	}
	if r.Truncated || bodyless(r.Method, r.Status) {
		return
	}
	misfits, total, checked := doc.Validate(op, r.Status, r.Body, domain.MaxMisfits)
	if !checked || total == 0 {
		return
	}
	out.Misfits = misfits
	out.MoreMisfits = total - len(misfits)
	out.SchemaFail = c.o.Schema == Fail
}
