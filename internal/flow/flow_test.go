package flow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
)

const loginYAML = `apiVersion: semar/v1
kind: Flow
id: login
surface: android
description: Sign in with the test account
service: shop-app
app: shop-app
params:
  - name: email
  - name: password_env
    default: SHOP_PASSWORD
steps:
  - step: start
    mobile:
      do: launch
  - step: email
    mobile:
      do: type
      screen: SignIn
      element: email
      value: "{{email}}"
  - step: password
    mobile:
      do: type
      screen: SignIn
      element: password
      secret: env:SHOP_PASSWORD
  - step: submit
    mobile:
      do: tap
      screen: SignIn
      element: submit
    captures:
      - name: shown
        element: welcome
`

func writeFile(t *testing.T, repo, rel, text string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validFlow(t *testing.T) domain.Flow {
	t.Helper()
	f, err := flow.Parse([]byte(loginYAML), "login.yaml", "login")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParseReadsAFlow(t *testing.T) {
	f := validFlow(t)
	if f.ID != "login" || len(f.Steps) != 4 || len(f.Params) != 2 || f.Params[0].Required() != true || f.Params[1].Required() {
		t.Fatalf("flow = %+v", f)
	}
	steps := flow.Steps(f)
	if m := steps[1].Mobile; m.Service != "shop-app" || m.App != "shop-app" {
		t.Errorf("the step must take the service and app of the flow: %+v", m)
	}
	if f.Steps[1].Mobile.Service != "" {
		t.Error("Steps changed the flow")
	}
	if got := f.CaptureNames(); !slices.Equal(got, []string{"shown"}) {
		t.Errorf("captures = %v", got)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	f := validFlow(t)
	data, err := flow.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	again, err := flow.Parse(data, "login.yaml", "login")
	if err != nil {
		t.Fatalf("Parse(Marshal(f)): %v\n%s", err, data)
	}
	data2, err := flow.Marshal(again)
	if err != nil || string(data2) != string(data) {
		t.Errorf("second write differs: %v\n%s\n---\n%s", err, data, data2)
	}
	if flow.Digest(data) != flow.Digest(data2) || !strings.HasPrefix(flow.Digest(data), "sha256:") || len(flow.Digest(data)) != 7+64 {
		t.Errorf("digest = %s", flow.Digest(data))
	}
	if flow.Digest(data) == flow.Digest([]byte("x")) {
		t.Error("digest does not depend on the bytes")
	}
}

func TestMarshalRefusesAFlowThatValidateRefuses(t *testing.T) {
	f := validFlow(t)
	f.Surface = domain.SurfaceWeb
	if _, err := flow.Marshal(f); err == nil || !strings.Contains(err.Error(), "android") {
		t.Errorf("err = %v", err)
	}
}

func TestValidateRefuses(t *testing.T) {
	many := func(n int) []domain.ScriptStep {
		out := make([]domain.ScriptStep, n)
		for i := range out {
			out[i] = domain.ScriptStep{Step: domain.StepID("s" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)), Mobile: &domain.MobileAction{Do: domain.MobilePress, Value: "back"}}
		}
		return out
	}
	tests := []struct {
		name string
		edit func(*domain.Flow)
		want string
	}{
		{"api version", func(f *domain.Flow) { f.APIVersion = "v2" }, "apiVersion must be semar/v1"},
		{"kind", func(f *domain.Flow) { f.Kind = "Script" }, "kind must be Flow"},
		{"id does not match the file", func(f *domain.Flow) { f.ID = "other" }, "does not match the file name login.yaml"},
		{"id is not a slug", func(f *domain.Flow) { f.ID = "a.b" }, "invalid flow ID"},
		{"web surface", func(f *domain.Flow) { f.Surface = domain.SurfaceWeb }, "surface must be android"},
		{"no steps", func(f *domain.Flow) { f.Steps = nil }, "at least one step"},
		{"too many steps", func(f *domain.Flow) { f.Steps = many(51) }, "51 steps; at most 50"},
		{"use with an action", func(f *domain.Flow) {
			f.Steps[0] = domain.ScriptStep{Step: "start", Use: "other", Mobile: &domain.MobileAction{Do: domain.MobileLaunch}}
		}, "has no action of its own"},
		{"use with captures", func(f *domain.Flow) {
			f.Steps[0] = domain.ScriptStep{Step: "start", Use: "other", Captures: []domain.Capture{{Name: "x", Element: "y"}}}
		}, "takes no captures"},
		{"with without use", func(f *domain.Flow) { f.Steps[0].With = map[string]string{"a": "b"} }, "with belongs to a step that has use"},
		{"with a variable that is not known", func(f *domain.Flow) {
			f.Steps[0] = domain.ScriptStep{Step: "start", Use: "other", With: map[string]string{"a": "{{nope}}"}}
		}, "{{nope}} is not a param"},
		{"a bad pattern", func(f *domain.Flow) { f.Steps[3].Captures[0].Pattern = "([a-z" }, "not a valid regular expression"},
		{"a long pattern", func(f *domain.Flow) { f.Steps[3].Captures[0].Pattern = strings.Repeat("a", 201) }, "longer than 200"},
		{"a request", func(f *domain.Flow) { f.Steps[0] = domain.ScriptStep{Step: "start", Request: &domain.APIRequest{}} }, "no request and no web action"},
		{"a web action", func(f *domain.Flow) { f.Steps[0] = domain.ScriptStep{Step: "start", Web: &domain.WebAction{}} }, "no request and no web action"},
		{"no action", func(f *domain.Flow) { f.Steps[0] = domain.ScriptStep{Step: "start"} }, "needs a mobile action"},
		{"duplicate step", func(f *domain.Flow) { f.Steps[1].Step = "start" }, "step start is defined more than once"},
		{"bad step name", func(f *domain.Flow) { f.Steps[1].Step = "no way" }, "step name"},
		{"no service", func(f *domain.Flow) { f.Service = "" }, "has no service"},
		{"no app", func(f *domain.Flow) { f.App = "" }, "has no app"},
		{"bad mobile action", func(f *domain.Flow) { f.Steps[1].Mobile.Do = domain.MobileTap }, "tap takes no value"},
		{"unknown variable", func(f *domain.Flow) { f.Steps[1].Mobile.Value = "{{nobody}}" }, "{{nobody}} is not a param"},
		{"scenario variable by name", func(f *domain.Flow) { f.Steps[1].Mobile.Value = "{{order.id}}" }, "{{order.id}} is not known in a flow"},
		{"later capture", func(f *domain.Flow) { f.Steps[1].Mobile.Value = "{{shown}}" }, "{{shown}} is not a param"},
		{"jsonpath capture", func(f *domain.Flow) { f.Steps[3].Captures[0] = domain.Capture{Name: "x", JSONPath: "$.a"} }, "no response to read"},
		{"capture named as a param", func(f *domain.Flow) { f.Steps[3].Captures[0].Name = "email" }, "defined more than once, or is the name of a param"},
		{"duplicate param", func(f *domain.Flow) { f.Params = append(f.Params, domain.FlowParam{Name: "email"}) }, "param email is defined more than once"},
		{"param with a hyphen", func(f *domain.Flow) { f.Params[0].Name = "e-mail" }, "param name"},
		{"default with a variable", func(f *domain.Flow) { v := "{{x}}"; f.Params[1].Default = &v }, "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validFlow(t)
			tt.edit(&f)
			ps := flow.Validate(f, "login")
			if !slices.ContainsFunc(ps, func(p flow.Problem) bool { return strings.Contains(p.String(), tt.want) }) {
				t.Errorf("problems = %v, want one holding %q", ps, tt.want)
			}
		})
	}
}

func TestTheScopeIsClosedButAllowsItsOwn(t *testing.T) {
	f := validFlow(t)
	// A param, an earlier capture, run.id and a date variable are all fine.
	f.Steps[1].Mobile.Value = "{{email}}-{{run.id}}-{{date.today}}"
	f.Steps = append(f.Steps, domain.ScriptStep{Step: "again", Mobile: &domain.MobileAction{Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "{{shown}}"}})
	if ps := flow.Validate(f, "login"); len(ps) != 0 {
		t.Errorf("problems = %v", ps)
	}
}

func TestParseRefusesUnknownFieldsAndBigFiles(t *testing.T) {
	if _, err := flow.Parse([]byte(loginYAML+"extra: 1\n"), "login.yaml", "login"); err == nil || !strings.Contains(err.Error(), "login.yaml") {
		t.Errorf("an unknown field: err = %v", err)
	}
	if _, err := flow.Parse([]byte(strings.Repeat("#", flow.MaxFileSize+1)), "login.yaml", "login"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a big file: err = %v", err)
	}
	if _, err := flow.Parse(nil, "login.yaml", "login"); err == nil {
		t.Error("an empty file was accepted")
	}
}

func TestLoadReadsAFlowAndItsDigest(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".semar/flows/login.yaml", loginYAML)
	e, err := flow.Load(repo, "login")
	if err != nil {
		t.Fatal(err)
	}
	if e.Flow.ID != "login" || e.Digest != flow.Digest([]byte(loginYAML)) {
		t.Errorf("entry = %+v", e)
	}
	all, err := flow.LoadAll(repo)
	if err != nil || len(all) != 1 || all["login"].Digest != e.Digest {
		t.Errorf("LoadAll = %v, %v", all, err)
	}
	src := flow.Loader(repo)
	f, digest, err := src.Flow("login")
	if err != nil || f.ID != "login" || digest != e.Digest {
		t.Errorf("Source.Flow = %v %q %v", f.ID, digest, err)
	}
	if _, err := src.Resolver()("login"); err != nil {
		t.Errorf("Resolver: %v", err)
	}
	if _, err := src.Resolver()("nope"); !errors.Is(err, flow.ErrNotFound) {
		t.Errorf("a missing flow: err = %v, want ErrNotFound", err)
	}
}

func TestLoadAllReportsABadFileAndKeepsTheGoodOnes(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".semar/flows/login.yaml", loginYAML)
	writeFile(t, repo, ".semar/flows/broken.yaml", "apiVersion: semar/v1\nkind: Flow\nid: broken\nsurface: web\nsteps: []\n")
	all, err := flow.LoadAll(repo)
	if err == nil || !strings.Contains(err.Error(), "broken.yaml") || len(all) != 1 {
		t.Errorf("all = %v, err = %v", all, err)
	}
}

func TestFlowFilesAreUntrusted(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		for _, repo := range []string{t.TempDir(), func() string { r := t.TempDir(); _ = os.MkdirAll(filepath.Join(r, ".semar", "flows"), 0o755); return r }()} {
			if _, err := flow.Load(repo, "login"); !errors.Is(err, flow.ErrNotFound) {
				t.Errorf("err = %v, want ErrNotFound", err)
			}
		}
	})
	t.Run("bad ids", func(t *testing.T) {
		for _, id := range []string{"", "../x", "a/b", ".hidden", "a.b", `a\b`} {
			if _, err := flow.Load(t.TempDir(), id); err == nil || errors.Is(err, flow.ErrNotFound) {
				t.Errorf("id %q: err = %v, want a refusal", id, err)
			}
		}
	})
	t.Run("too big", func(t *testing.T) {
		repo := t.TempDir()
		writeFile(t, repo, ".semar/flows/login.yaml", strings.Repeat("#", flow.MaxFileSize+1))
		if _, err := flow.Load(repo, "login"); err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a directory in place of the file", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, ".semar", "flows", "login.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := flow.Load(repo, "login"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("err = %v", err)
		}
	})
	if runtime.GOOS == "windows" {
		return
	}
	t.Run("a symbolic link as the file", func(t *testing.T) {
		repo := t.TempDir()
		outside := filepath.Join(t.TempDir(), "login.yaml")
		writeFile(t, filepath.Dir(outside), "login.yaml", loginYAML)
		if err := os.MkdirAll(filepath.Join(repo, ".semar", "flows"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, ".semar", "flows", "login.yaml")); err != nil {
			t.Skip(err)
		}
		if _, err := flow.Load(repo, "login"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a symbolic link as the directory", func(t *testing.T) {
		repo, elsewhere := t.TempDir(), t.TempDir()
		writeFile(t, elsewhere, "login.yaml", loginYAML)
		if err := os.MkdirAll(filepath.Join(repo, ".semar"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(repo, ".semar", "flows")); err != nil {
			t.Skip(err)
		}
		if _, err := flow.Load(repo, "login"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("err = %v", err)
		}
		if _, err := flow.Write(repo, validFlow(t)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("Write through a linked directory: err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(elsewhere, "login.yaml")); err != nil {
			t.Error("the file outside the repository was touched")
		}
	})
}

func TestSecretEnvNamesReadsEveryFlow(t *testing.T) {
	repo := t.TempDir()
	if got := flow.SecretEnvNames(repo); len(got) != 0 {
		t.Errorf("no flows: %v", got)
	}
	writeFile(t, repo, ".semar/flows/login.yaml", loginYAML)
	// A flow with a mistake still has its secrets kept from a harness.
	bad := strings.ReplaceAll(strings.ReplaceAll(loginYAML, "id: login", "id: bad"), "SHOP_PASSWORD", "BAD_PASSWORD")
	writeFile(t, repo, ".semar/flows/bad.yaml", strings.Replace(bad, "surface: android", "surface: web", 1))
	got := flow.SecretEnvNames(repo)
	slices.Sort(got)
	if !slices.Equal(got, []string{"BAD_PASSWORD", "SHOP_PASSWORD"}) {
		t.Errorf("names = %v", got)
	}
}

func TestWriteIsAtomicAndUndoIsOneShot(t *testing.T) {
	repo := t.TempDir()
	f := validFlow(t)
	saved, err := flow.Write(repo, f)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Path != ".semar/flows/login.yaml" {
		t.Errorf("path = %s", saved.Path)
	}
	data, err := os.ReadFile(filepath.Join(repo, saved.Path))
	if err != nil || flow.Digest(data) != saved.Digest {
		t.Fatalf("file digest = %s, saved = %s, err = %v", flow.Digest(data), saved.Digest, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(repo, ".semar", "flows")); len(entries) != 1 {
		t.Errorf("the directory holds %d entries: a temporary file was left", len(entries))
	}
	if err := saved.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, saved.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the new file is still there: %v", err)
	}
	if err := saved.Undo(); err == nil || !strings.Contains(err.Error(), "already undone") {
		t.Errorf("the second undo: err = %v, want it refused", err)
	}
}

func TestUndoPutsBackAFlowThatWasReplaced(t *testing.T) {
	repo := t.TempDir()
	first, err := flow.Write(repo, validFlow(t))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(repo, first.Path))
	changed := validFlow(t)
	changed.Description = "Another way in"
	second, err := flow.Write(repo, changed)
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest == first.Digest {
		t.Fatal("the flow did not change")
	}
	if err := second.Undo(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, second.Path))
	if string(after) != string(before) {
		t.Errorf("the replaced flow was not put back:\n%s", after)
	}
}

func TestUndoRefusesAFileThatChangedSinceTheSave(t *testing.T) {
	repo := t.TempDir()
	saved, err := flow.Write(repo, validFlow(t))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(repo, saved.Path)
	if err := os.WriteFile(p, []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saved.Undo(); err == nil || !strings.Contains(err.Error(), "changed since it was saved") {
		t.Errorf("err = %v, want a refusal", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "edited by hand\n" {
		t.Errorf("a later edit was lost: %q", got)
	}
}

func TestWriteRefusesAnInvalidFlow(t *testing.T) {
	repo := t.TempDir()
	f := validFlow(t)
	f.Steps = nil
	if _, err := flow.Write(repo, f); err == nil {
		t.Error("an invalid flow was written")
	}
	if _, err := os.Stat(filepath.Join(repo, ".semar")); err == nil {
		t.Error("a refused flow left a directory behind")
	}
}

func TestRunWithBeforeStopsAtAStepBoundary(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	var seen []string
	o := flow.Options{Before: func(_ context.Context, step string) error {
		seen = append(seen, step)
		if step == "login.password" {
			return errors.New("paused by a person before step " + step)
		}
		return nil
	}}
	res, err := flow.RunWith(t.Context(), eng, screenFiles{shopScreens}, validFlow(t), map[string]string{"email": "a@b.c"}, map[string]string{"run.id": "run-1"}, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != flow.StatusRefused || res.Error != "paused by a person before step login.password" {
		t.Fatalf("status = %q, error = %q", res.Status, res.Error)
	}
	if got := d.Typed(); len(got) != 1 || got[0] != "a@b.c" {
		t.Errorf("typed = %q: the steps before the boundary ran, and the one after did not", got)
	}
	if len(seen) == 0 || seen[len(seen)-1] != "login.password" {
		t.Errorf("Before saw %v", seen)
	}
}

func TestRunWithBeforeStopsACancelledRun(t *testing.T) {
	d := shop()
	eng, _, _ := engine(t, d, secrets{"env:SHOP_PASSWORD": "pw"}, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := flow.Options{Before: func(context.Context, string) error { return nil }}
	_, err := flow.RunWith(ctx, eng, screenFiles{shopScreens}, validFlow(t), map[string]string{"email": "a@b.c"}, map[string]string{"run.id": "run-1"}, o)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if len(d.Typed()) != 0 {
		t.Errorf("typed = %q after the cancel", d.Typed())
	}
}
