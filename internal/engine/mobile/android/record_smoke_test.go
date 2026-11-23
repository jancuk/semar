package android_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/secret"
	"github.com/jancuk/semar/internal/userconfig"
)

type smokeSink struct{}

func (s *smokeSink) WriteAction(a domain.Action) (string, error) {
	return "actions/" + string(a.ID) + ".json", nil
}
func (s *smokeSink) AppendActivity(domain.Activity) error { return nil }

type smokeFiles struct{}

func (smokeFiles) WriteFile(string, []byte) error { return nil }

type smokeScreens struct{ repo string }

func (s smokeScreens) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }

// TestSmokeRecordSettings runs a recording session against a real emulator and the Settings app. It is
// opt in, like TestSmokeAndroidSettings:
//
//	SEMAR_ANDROID_SMOKE=1 go test -run SmokeRecord -v ./internal/engine/mobile/android
//
// It records a tap and a key, adds a check, saves a scenario and then a flow, gives the device away for a
// verify, runs the flow on a clean session, takes the device back, and ends with no forward left behind.
// Only fakes can cover the rest, so this is the one test that shows that the lease is free in practice.
func TestSmokeRecordSettings(t *testing.T) {
	if os.Getenv("SEMAR_ANDROID_SMOKE") != "1" {
		t.Skip("set SEMAR_ANDROID_SMOKE=1 to run the Android recording smoke test")
	}
	home, err := userconfig.Home(os.LookupEnv)
	if err != nil {
		t.Skipf("no Semar home: %v", err)
	}
	for _, name := range []string{android.ServerAPKName, android.ServerTestAPKName} {
		if _, err := os.Stat(filepath.Join(home, "android", name)); err != nil {
			t.Skipf("the server APK %s is not in %s: %v", name, filepath.Join(home, "android"), err)
		}
	}
	adbBin, err := android.FindADB(os.LookupEnv, "")
	if err != nil {
		t.Skipf("no adb: %v", err)
	}
	var run android.ExecRunner
	ctx := context.Background()
	out, err := run.Output(ctx, adbBin, "devices", "-l")
	if err != nil {
		t.Skipf("adb devices: %v", err)
	}
	if _, un := android.ChooseDevice(android.ParseDevices(string(out)), os.Getenv("ANDROID_SERIAL")); un != nil {
		t.Skipf("no emulator to use: %s", un.Reason)
	}

	repo := t.TempDir()
	var mu sync.Mutex
	opens, open := 0, 0
	factory := func(_ context.Context, lookup mobilerec.LookupEnv) (mobilerec.Handle, error) {
		pol, err := effects.New(config.Environment{Hosts: []string{"settings.invalid"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}})
		if err != nil {
			return mobilerec.Handle{}, err
		}
		secrets := secret.NewEnv(lookup)
		red, err := redact.New(config.Redact{}, secrets.Loaded, []byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			return mobilerec.Handle{}, err
		}
		rec := recorder.New(&smokeSink{}, red, func(string, *url.URL) effects.Class { return effects.Read }, "smoke-1")
		p := android.New(android.Options{
			Home: home, RepoRoot: repo, LookupEnv: lookup,
			Apps: map[string]android.App{"settings": {Package: "com.android.settings", Reset: "restart"}},
		})
		eng := mobile.New(mobile.Options{
			Policy: pol, Recorder: rec, Files: smokeFiles{}, Secrets: secrets, Platform: p, Run: "smoke-1",
			Services: map[string]mobile.Service{"settings": {}},
		})
		mu.Lock()
		opens++
		open++
		mu.Unlock()
		return mobilerec.Handle{Engine: eng, Platform: p, Close: func(ctx context.Context) error {
			mu.Lock()
			open--
			mu.Unlock()
			return eng.Close(ctx)
		}}, nil
	}
	forwards := func() string {
		t.Helper()
		b, err := run.Output(ctx, adbBin, "forward", "--list")
		if err != nil {
			t.Fatalf("adb forward --list: %v", err)
		}
		return string(b)
	}

	s, err := mobilerec.New(mobilerec.Options{
		Repo: repo, Kind: mobilerec.KindScenario, ID: "SMOKE-1", Ticket: "SMOKE", Service: "settings", App: "settings",
		Open: factory, Lookup: os.LookupEnv, Settle: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Wipe()
	defer func() { _ = s.Close(ctx) }()

	// Start resets the app and launches it.
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(f.Elements) < 3 {
		t.Fatalf("the Settings screen has %d elements", len(f.Elements))
	}
	t.Logf("start: screen %q with %d elements on %s", f.Screen, len(f.Elements), s.Device())

	// Act: tap an entry that has a locator of its own, and go back.
	var entry *mobilerec.Element
	for i := range f.Elements {
		e := &f.Elements[i]
		if e.Clickable && !e.Editable && e.Locator != nil && (e.Text != "" || e.Desc != "") && e.Stability != "weak" {
			entry = e
			break
		}
	}
	if entry == nil {
		t.Skip("no entry of Settings has a strong locator on this emulator")
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: mobilerec.DoTap, Frame: f.ID, Target: &mobilerec.Target{Index: entry.Index}}); err != nil {
		t.Fatalf("tap: %v", err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: mobilerec.DoPress, Frame: f.ID, Value: "back"}); err != nil {
		t.Fatalf("press back: %v", err)
	}

	// Assert: a text that Settings shows. A check does nothing on the device.
	var text *mobilerec.Element
	for i := range f.Elements {
		if e := &f.Elements[i]; e.Locator != nil && e.Text != "" && e.Stability != "weak" {
			text = e
			break
		}
	}
	if text == nil {
		t.Skip("no text of Settings has a strong locator on this emulator")
	}
	if _, err = s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: mobilerec.Target{Index: text.Index}, Kind: mobilerec.AssertExists}); err != nil {
		t.Fatalf("assert: %v", err)
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatalf("save the scenario: %v", err)
	}
	if !strings.HasSuffix(sv.ScenarioPath, "SMOKE-1.feature") {
		t.Errorf("saved %v", sv.Files)
	}

	// The same steps, without the check, as a flow: a flow is what a verify can run alone.
	if err := s.DeleteLastStep(); err != nil {
		t.Fatal(err)
	}
	if err := s.Retarget(mobilerec.Next{Kind: mobilerec.KindFlow, ID: "smoke-settings"}); err != nil {
		t.Fatal(err)
	}
	if sv, err = s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatalf("save the flow: %v", err)
	}
	if sv.FlowPath != ".semar/flows/smoke-settings.yaml" {
		t.Errorf("saved %v", sv.Files)
	}

	// Suspend gives the device away: the server of the first session is gone, and a second one can start.
	if err := s.Suspend(ctx); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if got := forwards(); strings.Contains(got, "tcp:6790") {
		t.Errorf("a forward was left behind by the recording:\n%s", got)
	}
	entryFlow, err := flow.Load(repo, "smoke-settings")
	if err != nil {
		t.Fatal(err)
	}
	h, err := factory(ctx, s.Overlay().Lookup)
	if err != nil {
		t.Fatal(err)
	}
	res, err := flow.Run(ctx, h.Engine, smokeScreens{repo}, entryFlow.Flow, nil, map[string]string{"run.id": "smoke"})
	if cerr := h.Close(ctx); cerr != nil {
		t.Errorf("close the verify session: %v", cerr)
	}
	if err != nil || res.Status != flow.StatusCompleted {
		t.Fatalf("the flow on a clean session: %v, %+v", err, res)
	}

	// The device comes back, and Close leaves nothing behind.
	if _, err := s.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := forwards(); strings.Contains(got, "tcp:6790") {
		t.Errorf("a forward was left behind:\n%s", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if open != 0 || opens != 3 {
		t.Errorf("%d sessions are open, and %d were opened: want 0, and the recording, the verify, and the return", open, opens)
	}
}
