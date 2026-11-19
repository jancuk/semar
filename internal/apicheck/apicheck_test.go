package apicheck_test

import (
	"testing"
	"time"

	"github.com/jancuk/semar/internal/apicheck"
	"github.com/jancuk/semar/internal/openapi"
)

const doc = `
openapi: 3.0.3
info: {title: shop, version: "1"}
servers: [{url: "https://shop.example/api"}]
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

func source(t *testing.T, service string) apicheck.Source {
	t.Helper()
	d, err := openapi.Parse([]byte(doc), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return apicheck.Source{Service: service, Doc: d}
}

func resp(status int, body string, ms int64) apicheck.Response {
	return apicheck.Response{Service: "orders", Method: "GET", Path: "/orders/42", FullPath: "/api/orders/42", Status: status, Body: []byte(body), DurationMS: ms}
}

func TestNothingToCheckGivesNoChecker(t *testing.T) {
	if apicheck.New(apicheck.Options{}) != nil {
		t.Error("a checker with no document and no limit")
	}
	if apicheck.New(apicheck.Options{Schema: apicheck.Off, Docs: []apicheck.Source{source(t, "")}}) != nil {
		t.Error("a checker with the schema check off")
	}
	var c *apicheck.Checker
	if c.Check(resp(200, `{}`, 1)) != nil {
		t.Error("a nil checker found something")
	}
}

func TestMisfitFailsByDefaultAndWarnsOnRequest(t *testing.T) {
	for _, tc := range []struct {
		mode string
		fail bool
	}{{"", true}, {apicheck.Fail, true}, {apicheck.Warn, false}} {
		c := apicheck.New(apicheck.Options{Env: "shop", Schema: tc.mode, Docs: []apicheck.Source{source(t, "orders")}})
		got := c.Check(resp(200, `{"id":"x"}`, 5))
		if got == nil || len(got.Misfits) != 1 || got.SchemaFail != tc.fail || got.Operation != "GET /orders/{id}" || got.Env != "shop" {
			t.Errorf("mode %q: %+v", tc.mode, got)
		}
	}
}

func TestFittingResponseGivesNothing(t *testing.T) {
	c := apicheck.New(apicheck.Options{Docs: []apicheck.Source{source(t, "")}})
	if got := c.Check(resp(200, `{"id":1}`, 5)); got != nil {
		t.Errorf("%+v", got)
	}
}

func TestUndeclaredStatusIsAWarningFlag(t *testing.T) {
	c := apicheck.New(apicheck.Options{Docs: []apicheck.Source{source(t, "")}})
	got := c.Check(resp(418, `{"x":1}`, 5))
	if got == nil || !got.Undeclared || got.Status != 418 || len(got.Misfits) != 0 || got.SchemaFail {
		t.Errorf("%+v", got)
	}
}

func TestNoOperationOrAnotherServiceChecksNothing(t *testing.T) {
	c := apicheck.New(apicheck.Options{Docs: []apicheck.Source{source(t, "billing")}})
	if got := c.Check(resp(200, `{"id":"x"}`, 5)); got != nil {
		t.Errorf("a document of another service was used: %+v", got)
	}
	c = apicheck.New(apicheck.Options{Docs: []apicheck.Source{source(t, "")}})
	r := resp(200, `{"id":"x"}`, 5)
	r.Path, r.FullPath = "/unknown", "/api/unknown"
	if got := c.Check(r); got != nil {
		t.Errorf("a request with no operation was judged: %+v", got)
	}
}

func TestTruncatedAndBodylessResponsesSkipTheSchema(t *testing.T) {
	c := apicheck.New(apicheck.Options{Docs: []apicheck.Source{source(t, "")}})
	r := resp(200, `{"id":`, 5)
	r.Truncated = true
	if got := c.Check(r); got != nil {
		t.Errorf("truncated: %+v", got)
	}
}

func TestTimeLimit(t *testing.T) {
	c := apicheck.New(apicheck.Options{Limit: 2 * time.Second})
	if got := c.Check(resp(200, `{}`, 2000)); got != nil {
		t.Errorf("at the limit: %+v", got)
	}
	got := c.Check(resp(200, `{}`, 2300))
	if got == nil || !got.Slow() || got.TookMS != 2300 || got.LimitMS != 2000 || got.SlowFail {
		t.Errorf("default is a warning: %+v", got)
	}
	c = apicheck.New(apicheck.Options{Limit: time.Second, LimitOn: apicheck.Fail})
	if got := c.Check(resp(200, `{}`, 1500)); got == nil || !got.SlowFail {
		t.Errorf("on fail: %+v", got)
	}
}
