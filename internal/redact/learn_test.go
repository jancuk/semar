package redact

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

func learner(t *testing.T) *Redactor {
	t.Helper()
	r, err := New(config.Redact{JSONPaths: []string{"$.v"}}, func() []string { return nil }, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func bodyAction(v string) domain.Action {
	return domain.Action{Response: &domain.HTTPResponse{Status: 200, Body: []byte(`{"v":"` + v + `"}`)}}
}

// A value that the response body of an action shows under a JSONPath is removed from the request
// of that same action too.
func TestJSONPathValueInTheSameAction(t *testing.T) {
	r := learner(t)
	a := bodyAction("3201770011")
	a.Request = &domain.HTTPRequest{Method: "GET", URL: "https://h/v1/customers/3201770011"}
	got := r.Action(a)
	if want := "https://h/v1/customers/" + r.Marker("3201770011"); got.Request.URL != want {
		t.Errorf("URL = %s, want %s", got.Request.URL, want)
	}
}

// The values kept for the run are bounded in count and size, and the first ones seen are kept.
func TestLearnedValuesAreBounded(t *testing.T) {
	r := learner(t)
	for i := range maxLearned + 10 {
		r.Action(bodyAction(fmt.Sprintf("value-%04d", i)))
	}
	r.Action(bodyAction("short"))
	r.Action(bodyAction(strings.Repeat("x", maxLearnedLen+1)))
	if n := len(r.learnedValues()); n != maxLearned {
		t.Fatalf("learned %d values, want %d", n, maxLearned)
	}
	probe := func(v string) string {
		return r.Action(domain.Action{Err: "saw " + v}).Err
	}
	if got := probe("value-0000"); got != "saw "+r.Marker("value-0000") {
		t.Errorf("the first value is not kept: %s", got)
	}
	if got := probe(fmt.Sprintf("value-%04d", maxLearned+5)); got != fmt.Sprintf("saw value-%04d", maxLearned+5) {
		t.Errorf("a value past the bound was kept: %s", got)
	}
	if got := probe("short"); got != "saw short" {
		t.Errorf("a value shorter than %d bytes was kept: %s", minSecretLen, got)
	}
}

// Two redactors that see the same actions keep the same values: the set does not depend on map order.
func TestLearnedValuesAreDeterministic(t *testing.T) {
	a := domain.Action{Response: &domain.HTTPResponse{Status: 200, Body: []byte(`{"v":["alpha-1","beta-22","gamma-333"]}`)}}
	r1, r2 := learner(t), learner(t)
	r1.Action(a)
	r2.Action(a)
	if fmt.Sprint(r1.learnedValues()) != fmt.Sprint(r2.learnedValues()) {
		t.Errorf("%v != %v", r1.learnedValues(), r2.learnedValues())
	}
}
