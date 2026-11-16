package obligation_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/obligation"
)

var t0 = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

func deferral(user string, at time.Time, reason string) obligation.Deferral {
	return obligation.Deferral{Obligation: "SHOP-482/difference-1", User: user, At: at, Reason: reason, Until: time.Date(2026, 12, 1, 23, 59, 59, 0, time.UTC)}
}

func result(user string, at time.Time, res string) obligation.ManualResult {
	return obligation.ManualResult{Obligation: "SHOP-482/difference-1", Result: res, User: user, At: at, Notes: "n"}
}

func TestDeferAndReadNewest(t *testing.T) {
	root := t.TempDir()
	s := obligation.NewStore(root)
	if got, _, err := s.Deferrals(now); err != nil || len(got) != 0 {
		t.Fatalf("an empty store gave %v, %v", got, err)
	}
	if err := s.Defer(deferral("tester", t0, "first")); err != nil {
		t.Fatal(err)
	}
	if err := s.Defer(deferral("other", t0.Add(time.Hour), "second")); err != nil {
		t.Fatal(err)
	}
	other := deferral("tester", t0, "elsewhere")
	other.Obligation = "refunds/a"
	if err := s.Defer(other); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Deferrals(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d obligations: %+v", len(got), got)
	}
	d := got["SHOP-482/difference-1"]
	if d.Reason != "second" || d.User != "other" || d.Schema != 1 || d.Identity != "local, unverified identity" {
		t.Errorf("newest deferral is %+v", d)
	}
	if got["refunds/a"].Reason != "elsewhere" {
		t.Errorf("other obligation: %+v", got["refunds/a"])
	}
	dir := filepath.Join(root, "store", "obligations", "SHOP-482", "difference-1", "deferral")
	if _, err := os.Stat(filepath.Join(dir, "tester-"+itoa(t0.Unix())+".json")); err != nil {
		t.Errorf("the record is not where the contract says: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory: %v, %v", info, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestRecordNeverReplaces(t *testing.T) {
	root := t.TempDir()
	s := obligation.NewStore(root)
	if err := s.Record(result("tester", t0, "failed")); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(result("tester", t0.Add(time.Minute), "passed")); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Manual(now)
	if err != nil {
		t.Fatal(err)
	}
	if m := got["SHOP-482/difference-1"]; m.Result != "passed" {
		t.Errorf("newest is %+v", m)
	}
	files, err := filepath.Glob(filepath.Join(root, "store", "obligations", "SHOP-482", "difference-1", "manual", "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("files %v, %v", files, err)
	}
}

func TestSameSecondDoesNotReplace(t *testing.T) {
	s := obligation.NewStore(t.TempDir())
	for _, res := range []string{"failed", "passed"} {
		if err := s.Record(result("tester", t0, res)); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := s.Manual(now)
	if err != nil {
		t.Fatal(err)
	}
	if got["SHOP-482/difference-1"].Result != "passed" {
		t.Errorf("the later record of one second must count: %+v", got)
	}
}

func TestStoreRefusesBadRecords(t *testing.T) {
	s := obligation.NewStore(t.TempDir())
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"id with a traversal", s.Defer(func() obligation.Deferral { d := deferral("u", t0, "r"); d.Obligation = "../x"; return d }()), "invalid"},
		{"id with one part", s.Defer(func() obligation.Deferral { d := deferral("u", t0, "r"); d.Obligation = "x"; return d }()), "<group>/<name>"},
		{"no user", s.Defer(deferral("", t0, "r")), "user"},
		{"no reason", s.Defer(deferral("u", t0, " ")), "reason"},
		{"no expiry", s.Defer(func() obligation.Deferral { d := deferral("u", t0, "r"); d.Until = time.Time{}; return d }()), "until"},
		{"result is neither", s.Record(result("u", t0, "maybe")), "passed or failed"},
		{"no user on a result", s.Record(result("", t0, "passed")), "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil || !strings.Contains(tt.err.Error(), tt.want) {
				t.Fatalf("got %v, want an error with %q", tt.err, tt.want)
			}
		})
	}
}

func TestStoreRefusesLinkSwapAndWrongDirectory(t *testing.T) {
	setup := func(t *testing.T) (string, *obligation.Store, string) {
		root := t.TempDir()
		s := obligation.NewStore(root)
		if err := s.Defer(deferral("tester", t0, "r")); err != nil {
			t.Fatal(err)
		}
		return root, s, filepath.Join(root, "store", "obligations", "SHOP-482", "difference-1", "deferral")
	}
	record := `{"schema": 1, "obligation": "SHOP-482/difference-1", "user": "x", "identity": "i", "at": "2026-11-17T09:00:00Z", "reason": "r", "until": "2026-12-01T23:59:59Z"}`

	t.Run("a link as a record", func(t *testing.T) {
		_, s, dir := setup(t)
		target := filepath.Join(t.TempDir(), "r.json")
		if err := os.WriteFile(target, []byte(record), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "x-1.json")); err != nil {
			t.Skip(err)
		}
		if _, _, err := s.Deferrals(now); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a link as a directory on the way", func(t *testing.T) {
		root, s, _ := setup(t)
		group := filepath.Join(root, "store", "obligations", "SHOP-482")
		if err := os.Rename(group, group+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(group+".real", group); err != nil {
			t.Skip(err)
		}
		if _, _, err := s.Deferrals(now); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("read: %v", err)
		}
		if err := s.Defer(deferral("again", t0.Add(time.Hour), "r")); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("write: %v", err)
		}
		entries, _ := os.ReadDir(filepath.Join(group+".real", "difference-1", "deferral"))
		if len(entries) != 1 {
			t.Errorf("the write went through the link: %v", entries)
		}
	})
	t.Run("a link as the store", func(t *testing.T) {
		root, s, _ := setup(t)
		dir := filepath.Join(root, "store", "obligations")
		if err := os.Rename(dir, dir+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dir+".real", dir); err != nil {
			t.Skip(err)
		}
		if _, _, err := s.Manual(now); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a record in the wrong directory", func(t *testing.T) {
		_, s, dir := setup(t)
		wrong := strings.Replace(record, "SHOP-482/difference-1", "SHOP-482/other", 1)
		if err := os.WriteFile(filepath.Join(dir, "x-1.json"), []byte(wrong), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Deferrals(now); err == nil || !strings.Contains(err.Error(), "SHOP-482/other") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an unknown field", func(t *testing.T) {
		_, s, dir := setup(t)
		bad := strings.Replace(record, `"reason"`, `"state": "verified", "reason"`, 1)
		if err := os.WriteFile(filepath.Join(dir, "x-1.json"), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Deferrals(now); err == nil || !strings.Contains(err.Error(), "state") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("another schema", func(t *testing.T) {
		_, s, dir := setup(t)
		if err := os.WriteFile(filepath.Join(dir, "x-1.json"), []byte(strings.Replace(record, `"schema": 1`, `"schema": 2`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Deferrals(now); err == nil || !strings.Contains(err.Error(), "schema 2") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not a regular file", func(t *testing.T) {
		_, s, dir := setup(t)
		if err := os.Mkdir(filepath.Join(dir, "x-1.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Deferrals(now); err != nil {
			t.Fatalf("a directory is skipped as in the confirmations: %v", err)
		}
	})
	t.Run("strays are ignored", func(t *testing.T) {
		root, s, dir := setup(t)
		for _, p := range []string{filepath.Join(dir, ".tmp-1"), filepath.Join(root, "store", "obligations", ".DS_Store"), filepath.Join(root, "store", "obligations", "SHOP-482", "note.txt")} {
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got, _, err := s.Deferrals(now); err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

func TestUserNameCleaned(t *testing.T) {
	root := t.TempDir()
	s := obligation.NewStore(root)
	if err := s.Defer(deferral("../x", t0, "r")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "store", "obligations", "SHOP-482", "difference-1", "deferral")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v, %v", entries, err)
	}
	if want := ".._x-" + itoa(t0.Unix()) + ".json"; entries[0].Name() != want {
		t.Errorf("file is %q, want %q", entries[0].Name(), want)
	}
	if err := s.Defer(deferral("..", t0.Add(time.Hour), "r")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "unknown-"+itoa(t0.Add(time.Hour).Unix())+".json")); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.Deferrals(now); err != nil || got["SHOP-482/difference-1"].User != ".." {
		t.Errorf("the user name in the record keeps its text: %+v, %v", got, err)
	}
}

// plant writes a record file as a committed repository could hold it.
func plant(t *testing.T, root, kind, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "store", "obligations", "SHOP-482", "difference-1", kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func deferralJSON(user, at, reason, until string) string {
	return `{"schema": 1, "obligation": "SHOP-482/difference-1", "user": "` + user + `", "identity": "i", "at": "` + at + `", "reason": "` + reason + `", "until": "` + until + `"}`
}

func TestDeferralsLeaveOutRecordsThatTheWriterWouldRefuse(t *testing.T) {
	at := "2026-11-17T09:00:00Z"
	name := "x-" + itoa(t0.Unix()) + ".json"
	tests := []struct {
		name, file, body, want string
	}{
		{"a far until", name, deferralJSON("x", at, "r", "9999-01-01T23:59:59Z"), "days"},
		{"181 days after the day", name, deferralJSON("x", at, "r", "2027-05-17T00:00:00Z"), "days"},
		{"no user", "-" + itoa(t0.Unix()) + ".json", deferralJSON("", at, "r", "2026-12-01T23:59:59Z"), "user"},
		{"no reason", name, deferralJSON("x", at, "", "2026-12-01T23:59:59Z"), "reason"},
		{"a reason of two lines", name, deferralJSON("x", at, `a\nb`, "2026-12-01T23:59:59Z"), "one line"},
		{"made in the future", "x-4070908800.json", deferralJSON("x", "2099-01-01T00:00:00Z", "r", "2099-02-01T23:59:59Z"), "after now"},
		{"a name that says another time", "x-1.json", deferralJSON("x", at, "r", "2026-12-01T23:59:59Z"), "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			plant(t, root, "deferral", tt.file, tt.body)
			got, problems, err := obligation.NewStore(root).Deferrals(now)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Errorf("the record counts: %+v", got)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tt.want) || !strings.Contains(problems[0], tt.file) {
				t.Errorf("problems = %q, want one naming the file and %q", problems, tt.want)
			}
		})
	}
}

func TestADeferralThatIsRefusedDoesNotHideAValidOne(t *testing.T) {
	root := t.TempDir()
	s := obligation.NewStore(root)
	if err := s.Defer(deferral("tester", t0, "valid")); err != nil {
		t.Fatal(err)
	}
	plant(t, root, "deferral", "x-"+itoa(t0.Unix())+".json", deferralJSON("x", "2026-11-17T09:00:00Z", "", "2026-12-01T23:59:59Z"))
	got, problems, err := s.Deferrals(now)
	if err != nil || len(problems) != 1 || got["SHOP-482/difference-1"].Reason != "valid" {
		t.Fatalf("got %+v, %q, %v", got, problems, err)
	}
}

func TestDeferRefusesMoreThan180Days(t *testing.T) {
	d := deferral("u", t0, "r")
	d.Until = t0.AddDate(0, 0, 182)
	if err := obligation.NewStore(t.TempDir()).Defer(d); err == nil || !strings.Contains(err.Error(), "days") {
		t.Fatalf("got %v", err)
	}
}

func manualJSON(res, at string) string {
	return `{"schema": 1, "obligation": "SHOP-482/difference-1", "result": "` + res + `", "user": "x", "identity": "i", "at": "` + at + `", "notes": "n"}`
}

func TestManualFromTheFutureDoesNotShadowALaterFailure(t *testing.T) {
	root := t.TempDir()
	s := obligation.NewStore(root)
	plant(t, root, "manual", "x-4070908800.json", manualJSON("passed", "2099-01-01T00:00:00Z"))
	if err := s.Record(result("tester", t0, "failed")); err != nil {
		t.Fatal(err)
	}
	got, problems, err := s.Manual(now)
	if err != nil {
		t.Fatal(err)
	}
	if m := got["SHOP-482/difference-1"]; m.Result != "failed" {
		t.Errorf("the newest result is %+v, want the failed one", m)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "after now") {
		t.Errorf("problems = %q", problems)
	}
}

func TestManualLeavesOutBadRecords(t *testing.T) {
	name := "x-" + itoa(t0.Unix()) + ".json"
	tests := []struct {
		name, file, body, want string
	}{
		{"a result that is neither", name, manualJSON("maybe", "2026-11-17T09:00:00Z"), "passed or failed"},
		{"a name that says another time", "x-5.json", manualJSON("passed", "2026-11-17T09:00:00Z"), "name"},
		{"a name of another user", "y-" + itoa(t0.Unix()) + ".json", manualJSON("passed", "2026-11-17T09:00:00Z"), "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			plant(t, root, "manual", tt.file, tt.body)
			got, problems, err := obligation.NewStore(root).Manual(now)
			if err != nil || len(got) != 0 || len(problems) != 1 || !strings.Contains(problems[0], tt.want) {
				t.Fatalf("got %+v, %q, %v", got, problems, err)
			}
		})
	}
}

func TestRecordsMadeByTheWriterHaveNoProblem(t *testing.T) {
	root := t.TempDir()
	s := obligation.NewStore(root)
	for i := 0; i < 3; i++ {
		if err := s.Record(result("a user", t0, "passed")); err != nil { // the same second: -2 and -3 follow
			t.Fatal(err)
		}
		if err := s.Defer(deferral("a user", t0, "r")); err != nil {
			t.Fatal(err)
		}
	}
	if _, problems, err := s.Manual(now); err != nil || len(problems) != 0 {
		t.Errorf("manual: %q, %v", problems, err)
	}
	if _, problems, err := s.Deferrals(now); err != nil || len(problems) != 0 {
		t.Errorf("deferrals: %q, %v", problems, err)
	}
}
