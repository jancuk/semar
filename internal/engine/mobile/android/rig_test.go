package android_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

const (
	pkgShop   = "com.example.shop"
	emulator  = "emulator-5554"
	emuLine   = "emulator-5554\tdevice product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1"
	serverPkg = "io.appium.uiautomator2.server"
	testPkg   = "io.appium.uiautomator2.server.test"
)

// clock is a clock that only a sleep moves, so a test of a timeout takes no time.
type clock struct {
	mu  sync.Mutex
	now time.Time
	n   int
	// real is a pause that a sleep also takes, for a test that waits for a process.
	real time.Duration
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.n++
	real := c.real
	c.mu.Unlock()
	if real > 0 {
		time.Sleep(real)
	}
	return nil
}

type rig struct {
	t      *testing.T
	adb    *mobiletest.ADB
	server *mobiletest.Server
	clk    *clock
	home   string
	repo   string
	env    map[string]string
	opts   android.Options
	// serverOpts is how the fake server speaks, which a rigOption may change.
	serverOpts mobiletest.Options
}

type rigOption func(*rig, *mobiletest.ADBConfig)

// newRig makes a fake adb and a fake server, and the options of a platform that uses them. The device is
// an emulator that has both server packages and the app.
func newRig(t *testing.T, mods ...rigOption) *rig {
	t.Helper()
	r := &rig{t: t, clk: &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, home: t.TempDir(), repo: t.TempDir(), env: map[string]string{}}
	r.serverOpts = mobiletest.Options{NoExecute: true}
	cfg := mobiletest.ADBConfig{
		Devices:   []string{emuLine},
		Installed: map[string]string{serverPkg: "/data/app/s/base.apk", testPkg: "/data/app/t/base.apk", pkgShop: "/data/app/shop/base.apk"},
	}
	for _, m := range mods {
		m(r, &cfg)
	}
	r.server = mobiletest.NewServer(t, r.serverOpts)
	u, _ := url.Parse(r.server.URL)
	cfg.Port, _ = strconv.Atoi(u.Port())
	r.adb = mobiletest.NewADB(t, cfg)
	r.env["SEMAR_ADB_BIN"] = r.adb.Path
	r.opts = android.Options{
		Home: r.home, RepoRoot: r.repo, Apps: map[string]android.App{"shop": {Package: pkgShop}},
		LookupEnv: func(k string) (string, bool) { v, ok := r.env[k]; return v, ok },
		Sleep:     r.clk.Sleep, Now: r.clk.Now,
	}
	return r
}

func (r *rig) platform() *android.Platform {
	r.t.Helper()
	p := android.New(r.opts)
	r.t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

// driver starts the platform and gives the driver of the shop app.
func (r *rig) driver(p *android.Platform) mobile.Driver {
	r.t.Helper()
	d, err := p.Driver(context.Background(), "shop")
	if err != nil {
		r.t.Fatalf("Driver: %v", err)
	}
	return d
}

func writeFile(t *testing.T, dir, name, content string) (path, sum string) {
	t.Helper()
	path = filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(s[:])
}

func unavailable(t *testing.T, err error) *mobile.Unavailable {
	t.Helper()
	var un *mobile.Unavailable
	if err == nil {
		t.Fatal("no error")
	}
	if !asUnavailable(err, &un) {
		t.Fatalf("error %v is not Unavailable", err)
	}
	return un
}

func asUnavailable(err error, target **mobile.Unavailable) bool {
	for err != nil {
		if u, ok := err.(*mobile.Unavailable); ok {
			*target = u
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
