package android

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
)

// imeTTL is how long the answer of InputMethod is kept.
const imeTTL = 10 * time.Second

type imeCache struct {
	valid bool
	at    time.Time
	pkg   string
	shown bool
}

// InputMethod says which keyboard (input method) the device uses, as a package, and whether it is
// showing on the screen. The answer is kept for ten seconds. LaunchApp, OpenURL, and Key drop it, and so
// does InvalidateInputMethod, which a caller uses when the windows on the screen changed.
func (c *Controller) InputMethod(ctx context.Context) (pkg string, shown bool, err error) {
	a, err := c.adb()
	if err != nil {
		return "", false, err
	}
	now := c.p.o.Now()
	c.mu.Lock()
	if c.ime.valid && now.Sub(c.ime.at) < imeTTL {
		pkg, shown = c.ime.pkg, c.ime.shown
		c.mu.Unlock()
		return pkg, shown, nil
	}
	c.mu.Unlock()
	setting, err := a.shell(ctx, "settings", "get", "secure", "default_input_method")
	if err != nil {
		return "", false, fmt.Errorf("the keyboard could not be read: %w", err)
	}
	dump, err := a.shell(ctx, "dumpsys", "input_method")
	if err != nil && strings.TrimSpace(dump) == "" {
		return "", false, fmt.Errorf("the keyboard could not be read: %w", err)
	}
	pkg, shown = ParseInputMethod(setting, dump)
	c.mu.Lock()
	c.ime = imeCache{valid: true, at: now, pkg: pkg, shown: shown}
	c.mu.Unlock()
	return pkg, shown, nil
}

// InvalidateInputMethod drops the answer that InputMethod kept.
func (c *Controller) InvalidateInputMethod() {
	c.mu.Lock()
	c.ime = imeCache{}
	c.mu.Unlock()
}

var (
	inputShownExpr   = regexp.MustCompile(`\b(?:mInputShown|isInputViewShown)=(true|false)\b`)
	imeWindowVisExpr = regexp.MustCompile(`\bmImeWindowVis=(0x[0-9a-fA-F]{1,8}|[0-9]{1,10})\b`)
)

// imeVisible is the bit of mImeWindowVis that says the window of the keyboard shows.
const imeVisible = 0x2

// ParseInputMethod reads the package of the keyboard from the output of settings get secure
// default_input_method (pkg/.Service, or null), and whether the keyboard shows from the output of
// dumpsys input_method. Android 12 and 13 write mInputShown=true, Android 14 and later write
// isInputViewShown=true for the service and a bit in mImeWindowVis, and a keyboard shows when any of them
// says so. It never fails: what it cannot read is "" and false.
func ParseInputMethod(setting, dump string) (pkg string, shown bool) {
	id := strings.TrimSpace(firstLine(setting))
	if p, _, ok := strings.Cut(id, "/"); ok && config.ValidAndroidPackage(p) {
		pkg = p
	}
	for _, m := range inputShownExpr.FindAllStringSubmatch(dump, -1) {
		if m[1] == "true" {
			return pkg, true
		}
	}
	for _, m := range imeWindowVisExpr.FindAllStringSubmatch(dump, -1) {
		v, err := strconv.ParseInt(m[1], 0, 64)
		if err == nil && v&imeVisible != 0 {
			return pkg, true
		}
	}
	return pkg, false
}
