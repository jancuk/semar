package mcpkit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
)

func TestGuardObserveRefusesWritesOnEverySurface(t *testing.T) {
	g := mcpkit.Guard{Observe: true, Reason: "the environment is production"}
	for _, s := range []mcpkit.Surface{"mobile", "web", "api", "journey", "control"} {
		if err := g.Allow(s, false); err != nil {
			t.Errorf("%s read: %v", s, err)
		}
		err := g.Allow(s, true)
		if err == nil || !errors.Is(err, mcpkit.ErrObserve) || !strings.Contains(err.Error(), "production") || !strings.Contains(err.Error(), string(s)) {
			t.Errorf("%s write: %v", s, err)
		}
	}
	if err := (mcpkit.Guard{}).Allow("mobile", true); err != nil {
		t.Errorf("no observe: %v", err)
	}
	if err := (mcpkit.Guard{Observe: true}).Allow("mobile", true); err == nil || !strings.Contains(err.Error(), "observe") {
		t.Errorf("no reason: %v", err)
	}
	if err := g.Allow("nope", false); err == nil {
		t.Error("an unknown surface was accepted")
	}
}

func TestGuardMiddlewareWrapsOnlyWrites(t *testing.T) {
	g := mcpkit.Guard{Observe: true, Reason: "read only"}
	ran := 0
	h := func(context.Context, mcp.Call) (mcp.Result, error) { ran++; return mcp.TextResult("ok"), nil }
	mw := g.Middleware()
	res, _ := mw(mcp.Tool{Surface: "mobile", Write: true}, h)(context.Background(), mcp.Call{})
	if !res.IsError || ran != 0 {
		t.Errorf("write: %+v ran=%d", res, ran)
	}
	res, _ = mw(mcp.Tool{Surface: "mobile"}, h)(context.Background(), mcp.Call{})
	if res.IsError || ran != 1 {
		t.Errorf("read: %+v ran=%d", res, ran)
	}
	res, _ = mw(mcp.Tool{Write: true}, h)(context.Background(), mcp.Call{})
	if res.IsError || ran != 2 {
		t.Errorf("no surface: %+v ran=%d", res, ran)
	}
}

func TestScrubberStandaloneAndLiveSet(t *testing.T) {
	s := mcpkit.NewScrubber(nil, "hunter2-secret")
	in := "password is hunter2-secret ok"
	out := s.Text(in)
	if strings.Contains(out, "hunter2-secret") || !strings.Contains(out, "[redacted") {
		t.Errorf("out = %q", out)
	}
	if !s.Leaks(in) || s.Leaks("nothing here") || s.Leaks("") {
		t.Error("Leaks is wrong")
	}
	if s.Text("") != "" {
		t.Error("empty text changed")
	}
	// A value that arrives later is found at once.
	late := "tok_9f8e7d6c5b"
	if s.Leaks("x " + late) {
		t.Fatal("unknown value flagged")
	}
	s.Add(late, "ab") // "ab" is too short and is ignored
	if got := s.Text("x " + late + " ab"); strings.Contains(got, late) || !strings.Contains(got, " ab") {
		t.Errorf("late = %q", got)
	}
	// The encoded forms are found too.
	if !s.Leaks("a=hunter2-secret&b=1") {
		t.Error("a query value")
	}
}

func TestScrubberIsSafeForConcurrentUse(t *testing.T) {
	s := mcpkit.NewScrubber(nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Add("value-" + strings.Repeat("x", i+4))
				_ = s.Text("value-xxxx and more")
				_ = s.Leaks("value-xxxxx")
			}
		}()
	}
	wg.Wait()
}

func newOutputs(t *testing.T) (*mcpkit.Outputs, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mcp")
	o, err := mcpkit.NewOutputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return o, dir
}

func TestOutputsCreatesTheFolderPrivateWithAGitignore(t *testing.T) {
	o, dir := newOutputs(t)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || strings.TrimSpace(string(b)) != "*" {
		t.Errorf(".gitignore = %q, %v", b, err)
	}
	if o.Root() != dir {
		t.Errorf("root = %s", o.Root())
	}
	// An empty .gitignore is filled; one with content is kept.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpkit.NewOutputs(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); strings.TrimSpace(string(b)) != "*" {
		t.Errorf("empty .gitignore became %q", b)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpkit.NewOutputs(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(b) != "keep\n" {
		t.Errorf("a .gitignore with content became %q", b)
	}
	if _, err := mcpkit.NewOutputs(""); err == nil {
		t.Error("an empty root")
	}
}

func TestOutputsResolveMatrix(t *testing.T) {
	o, dir := newOutputs(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "linkdir")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(dir, "link.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	png := []string{".png"}
	good := []string{"a.png", "sub/b.PNG", "new/deep/c.png", filepath.Join(dir, "d.png"), "./e.png"}
	for _, p := range good {
		got, err := o.Resolve(p, png)
		if err != nil {
			t.Errorf("%q refused: %v", p, err)
			continue
		}
		if !strings.HasPrefix(got, dir) && !strings.Contains(got, filepath.Base(dir)) {
			t.Errorf("%q resolved to %s, outside %s", p, got, dir)
		}
	}
	bad := map[string]string{
		"":                                "empty",
		"../x.png":                        "dotdot",
		"sub/../../x.png":                 "dotdot nested",
		outside + "/y.png":                "absolute outside",
		"/etc/passwd.png":                 "absolute elsewhere",
		"linkdir/z.png":                   "symlinked parent",
		"linkdir/new/z.png":               "symlinked parent, missing child",
		"link.png":                        "symlink target",
		"adir.png":                        "directory target",
		"a.txt":                           "bad extension",
		"noext":                           "no extension",
		"a\x00.png":                       "nul",
		".":                               "the folder itself",
		dir:                               "the root itself",
		filepath.Join(dir, "..", "q.png"): "absolute dotdot",
	}
	for p, why := range bad {
		if got, err := o.Resolve(p, png); err == nil {
			t.Errorf("%s: %q was accepted as %s", why, p, got)
		}
	}
	if _, err := o.Resolve("any.thing", nil); err != nil {
		t.Errorf("no extension list: %v", err)
	}
}

func TestOutputsWriteIsAtomicAndPrivate(t *testing.T) {
	o, dir := newOutputs(t)
	path, err := o.Write("shots/a.jpg", []string{".jpg", ".jpeg"}, []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "data" {
		t.Fatalf("read %v %q", err, b)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	if _, err := o.Write("shots/a.jpg", []string{".jpg"}, []byte("again")); err != nil {
		t.Errorf("overwrite a regular file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "again" {
		t.Errorf("contents = %q", b)
	}
	if _, err := o.Write("../escape.jpg", []string{".jpg"}, []byte("x")); err == nil {
		t.Error("an escape was written")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.jpg")); err == nil {
		t.Error("the escape file exists")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "shots"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("a temporary file is left: %s", e.Name())
		}
	}
}

func TestReadAPK(t *testing.T) {
	repo := t.TempDir()
	write := func(rel string, data []byte) string {
		p := filepath.Join(repo, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	app := write("build/app.apk", []byte("PK"))
	write("build/notes.txt", []byte("x"))
	write("build/empty.apk", nil)
	write("build/UPPER.APK", []byte("PK"))
	if err := os.Mkdir(filepath.Join(repo, "dir.apk"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(repo, "build", "link.apk")
	symOK := os.Symlink(app, linked) == nil

	for _, p := range []string{app, "build/app.apk", "build/UPPER.APK"} {
		if got, err := mcpkit.ReadAPK(p, repo); err != nil || !filepath.IsAbs(got) {
			t.Errorf("%q: %q, %v", p, got, err)
		}
	}
	bad := []string{"", "build/missing.apk", "build/notes.txt", "build/empty.apk", "dir.apk", "../app.apk", "a\x00.apk"}
	if symOK {
		bad = append(bad, "build/link.apk")
	}
	for _, p := range bad {
		if got, err := mcpkit.ReadAPK(p, repo); err == nil {
			t.Errorf("%q was accepted as %s", p, got)
		}
	}
	if _, err := mcpkit.ReadAPK("build/app.apk", ""); err == nil {
		t.Error("a relative path with no repository")
	}
	if got, err := mcpkit.ReadAPK(app, ""); err != nil || got != app {
		t.Errorf("an absolute path with no repository: %q, %v", got, err)
	}
}

func TestAgentAuthorIsOneShapeForEverySurface(t *testing.T) {
	for _, tc := range []struct{ client, model string }{
		{"claude-code", "claude-code"}, {"", "mcp"}, {"  \n\t ", "mcp"}, {"my\nclient\tname", "my client name"},
		{strings.Repeat("x", 200), strings.Repeat("x", 80)},
	} {
		got := mcpkit.AgentAuthor(tc.client)
		if got.Instance != "agent" || got.Harness != "mcp" || got.Model != tc.model {
			t.Errorf("AgentAuthor(%q) = %+v", tc.client, got)
		}
	}
}
