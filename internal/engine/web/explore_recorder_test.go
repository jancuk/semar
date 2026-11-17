package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/record"
)

// guardedRig is a recorder whose browser stays inside the hosts and the methods of an environment.
func guardedRig(t *testing.T, mux *http.ServeMux, methods ...string) (*recRig, *httptest.Server) {
	t.Helper()
	needChrome(t)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	policy, err := effects.New(config.Environment{Hosts: []string{u.Host}, Methods: methods, Effects: config.Effects{Mode: "test_data"}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := web.StartRecorder(context.Background(), web.RecorderOptions{
		Home: t.TempDir(), Environment: "test", Account: "default", Hidden: true, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Finish() })
	return &recRig{t: t, rec: rec, srv: srv}, srv
}

func TestAnExplorerStaysInsideTheHostsOfTheEnvironment(t *testing.T) {
	var outside atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1) }))
	defer other.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><title>Guard</title><p id="out">waiting</p><script>
Promise.allSettled([fetch(%q), fetch('/thing')]).then(r => {
  document.getElementById('out').textContent = r.map(x => x.status).join(',');
});</script>`, other.URL)
	})
	mux.HandleFunc("/thing", func(w http.ResponseWriter, r *http.Request) {})
	r, _ := guardedRig(t, mux, "GET")
	r.open("/p")
	x := web.NewExplorer(r.rec)
	deadline := time.Now().Add(10 * time.Second)
	for {
		l, err := x.List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(l.String(), "rejected,fulfilled") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page did not finish its requests as expected:\n%s", l.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if outside.Load() != 0 {
		t.Errorf("the browser sent %d requests to a host that the environment does not list", outside.Load())
	}
}

func TestSettleWaitsForTheRequestsOfThePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Slow</title><button onclick="fetch('/slow').then(r => r.text()).then(t => { const p = document.createElement('p'); p.textContent = t; document.body.append(p); })">Load</button>`)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(w, "loaded at last")
	})
	r, _ := guardedRig(t, mux, "GET")
	x, l := r.explore("/p")
	r.must(x.Click(t.Context(), refOf(t, l, "button", "Load")))
	r.rec.Settle(t.Context())
	l2, err := x.List(t.Context())
	if err != nil || !strings.Contains(l2.String(), "loaded at last") {
		t.Errorf("the page after a settle: %v\n%s", err, l2.String())
	}
}

func TestReopenStartsTheRecordingOverAtTheFirstPage(t *testing.T) {
	r := newRecRig(t, map[string]string{"/refund": refundPage})
	x, l := r.explore("/refund")
	r.must(x.Fill(t.Context(), refOf(t, l, "textbox", "Amount"), "5"))
	if _, err := x.Check(t.Context(), refOf(t, l, "text", "idle")); err != nil {
		t.Fatal(err)
	}
	if err := r.rec.Reopen(r.srv.URL + "/refund"); err != nil {
		t.Fatal(err)
	}
	r.waitSettled()
	time.Sleep(300 * time.Millisecond)
	evs := r.rec.Events()
	if len(evs) != 1 || evs[0].Kind != record.KindOpen || !strings.HasSuffix(evs[0].Page.URL, "/refund") {
		t.Errorf("after a reopen the events are %s, want one open", describeEvents(evs))
	}
}
