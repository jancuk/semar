package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/apicheck"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/openapi"
)

const orderDoc = `
openapi: 3.0.3
info: {title: orders, version: "1"}
paths:
  /orders/{id}:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}}}
`

func checker(t *testing.T, o apicheck.Options) *apicheck.Checker {
	t.Helper()
	d, err := openapi.Parse([]byte(orderDoc), "orders.yaml")
	if err != nil {
		t.Fatal(err)
	}
	o.Docs = []apicheck.Source{{Doc: d}}
	return apicheck.New(o)
}

func TestEngineStoresTheMisfitAndNoValue(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"value-of-the-response"}`))
	})
	e, sink := newEngine(t, srv, setup{options: func(o *api.Options) { o.Doc = checker(t, apicheck.Options{Env: "shop"}) }})
	res := do(t, e, get("/orders/42"))
	d := res.Action.Doc
	if d == nil || len(d.Misfits) != 1 || d.Misfits[0].Path != "$.id" || d.Misfits[0].Got != "string" || !d.SchemaFail {
		t.Fatalf("doc = %+v", d)
	}
	if stored := sink.last(t).Doc; stored == nil || stored.Operation != "GET /orders/{id}" {
		t.Errorf("stored doc = %+v", stored)
	}
	if strings.Contains(strings.Join([]string{d.Misfits[0].Path, d.Misfits[0].Want, d.Misfits[0].Got}, " "), "value-of") {
		t.Error("the misfit holds a value of the response")
	}
}

func TestEngineChecksNothingWhenTheResponseFitsOrThereIsNoChecker(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"id":1}`)) })
	e, _ := newEngine(t, srv, setup{options: func(o *api.Options) { o.Doc = checker(t, apicheck.Options{}) }})
	if d := do(t, e, get("/orders/42")).Action.Doc; d != nil {
		t.Errorf("a fitting response gave %+v", d)
	}
	e, _ = newEngine(t, srv, setup{})
	if d := do(t, e, get("/orders/42")).Action.Doc; d != nil {
		t.Errorf("no checker gave %+v", d)
	}
}

func TestEngineNamesASlowResponseFromSendToEndOfBody(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(60 * time.Millisecond)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	e, _ := newEngine(t, srv, setup{options: func(o *api.Options) {
		o.Doc = checker(t, apicheck.Options{Limit: 20 * time.Millisecond, LimitOn: apicheck.Fail})
	}})
	d := do(t, e, get("/orders/42")).Action.Doc
	if d == nil || !d.Slow() || !d.SlowFail || d.TookMS < 60 || d.LimitMS != 20 {
		t.Fatalf("doc = %+v", d)
	}
}
