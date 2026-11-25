package config

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/idcheck"
)

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,127}$`)

// CheckModel accepts a model name that cannot be read as a flag or hold a control character.
func CheckModel(model string) error {
	if !modelPattern.MatchString(model) {
		return fmt.Errorf("%q is not a model name: use up to 128 letters, digits, and . _ : / [ ] -, starting with a letter or digit", model)
	}
	return nil
}

// ParseRung reads one step of a plan ladder, <provider id>:<model>, such as claude:haiku. A model
// name may hold a colon, so the provider ends at the first one.
func ParseRung(s string) (provider, model string, err error) {
	provider, model, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || provider == "" || model == "" {
		return "", "", fmt.Errorf("%q is not a step of a ladder: write <provider id>:<model>, such as claude:haiku", s)
	}
	if err := idcheck.Check("provider id", provider); err != nil {
		return "", "", fmt.Errorf("%q: %w", s, err)
	}
	if err := CheckModel(model); err != nil {
		return "", "", fmt.Errorf("%q: %w", s, err)
	}
	return provider, model, nil
}

var reasoningPattern = regexp.MustCompile(`^[a-z]{1,16}$`)

// CheckReasoning accepts a reasoning level that cannot be read as a flag or hold a control
// character: 1 to 16 lower-case letters. Each kind decides which levels it can pass on.
func CheckReasoning(level string) error {
	if !reasoningPattern.MatchString(level) {
		return fmt.Errorf("%q is not a reasoning level: use 1 to 16 lower-case letters, such as high", level)
	}
	return nil
}

// CheckDeny refuses a setup.deny entry that a harness could read as a flag or split into
// several. The person's file and the Claude driver both use it.
func CheckDeny(deny []string) error {
	for i, entry := range deny {
		var problem string
		switch {
		case strings.TrimSpace(entry) == "":
			problem = "is empty"
		case strings.HasPrefix(entry, "-"):
			problem = "starts with a dash"
		case strings.Contains(entry, ","):
			problem = "holds a comma; give each tool as its own entry"
		case strings.ContainsFunc(entry, unicode.IsControl):
			problem = "holds a control character"
		}
		if problem != "" {
			return fmt.Errorf("entry %d of setup.deny, %q, %s", i+1, entry, problem)
		}
	}
	return nil
}

func joinErrors(errs []error) error { return errors.Join(errs...) }

func validate(c Config, name string) error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{name}, args...)...))
	}
	if c.Version != 1 {
		add("version must be 1")
	}
	if c.Replay.Workers < 0 {
		add("replay.workers must not be negative")
	}
	if c.Replay.Retries < 0 || c.Replay.Retries > MaxRetries {
		add("replay.retries must be from 0 to %d", MaxRetries)
	}
	validatePlan(c, add)
	validateKnowledge(c, add)
	envNames := make([]string, 0, len(c.Environments))
	for n := range c.Environments {
		envNames = append(envNames, n)
	}
	slices.Sort(envNames)
	for _, n := range envNames {
		if err := idcheck.Check("environment name", n); err != nil {
			add("environments: %v", err)
		}
		validateEnvironment(n, c.Environments[n], c.AuthProfiles, add)
	}
	for _, n := range slices.Sorted(maps.Keys(c.AuthProfiles)) {
		if err := idcheck.Check("auth profile name", n); err != nil {
			add("authProfiles: %v", err)
		}
		if problem := descriptionProblem(c.AuthProfiles[n].Description); problem != "" {
			add("authProfiles.%s.description %s", n, problem)
		}
		validateWebSignIn(n, c.AuthProfiles[n].Web, add)
	}
	return joinErrors(errs)
}

var webPasswordPattern = regexp.MustCompile(`^env:[A-Za-z_][A-Za-z0-9_]*$`)

func validateWebSignIn(account string, w *WebSignIn, add func(string, ...any)) {
	if w == nil {
		return
	}
	p := "authProfiles." + account + ".web"
	if w.User == "" || w.Password == "" {
		add("%s needs both user and password", p)
		return
	}
	if problem := descriptionProblem(w.User); problem != "" {
		add("%s.user %s", p, problem)
	}
	if !webPasswordPattern.MatchString(w.Password) {
		add("%s.password must be a reference to an environment variable, env:NAME, and never the password itself", p)
	}
}

func validateEnvironment(name string, e Environment, profiles map[string]AuthProfile, add func(string, ...any)) {
	p := "environments." + name
	if e.Kind != KindTest && e.Kind != KindProduction {
		add("%s.kind must be %s or %s, got %q", p, KindTest, KindProduction, e.Kind)
	}
	if len(e.Hosts) == 0 && !standaloneAndroid(e) {
		add("%s.hosts needs at least one entry", p)
	}
	for i, h := range e.Hosts {
		if err := hostrule.CheckEntry(h); err != nil {
			add("%s.hosts[%d]: %v", p, i, err)
		}
	}
	if len(e.Methods) == 0 {
		add("%s.methods needs at least one entry", p)
	}
	if e.Effects.Mode != "test_data" && e.Effects.Mode != "read_only" {
		add("%s.effects.mode is required: test_data or read_only", p)
	}
	for i, s := range e.Services {
		sp := fmt.Sprintf("%s.services[%d]", p, i)
		if s.Name == "" {
			add("%s.name is required", sp)
		} else if err := idcheck.Check("service name", s.Name); err != nil {
			add("%s.name: %v", sp, err)
		}
		if s.BaseURL == "" && s.Android != nil && s.Web == nil {
			// An app on a device may have no address of its own: it is a standalone service.
		} else {
			u, err := url.Parse(s.BaseURL)
			switch {
			case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
				add("%s.baseUrl must be an absolute http or https URL, got %q", sp, s.BaseURL)
			case !hostrule.Allowed(u, e.Hosts):
				add("%s: baseUrl host %s is not in hosts", sp, originHost(u))
			}
		}
		if s.Web != nil {
			if st := s.Web.Start; !strings.HasPrefix(st, "/") || strings.HasPrefix(st, "//") || strings.ContainsAny(st, " \t\r\n\\") {
				add("%s.web.start must be a path below the base URL that starts with one /, got %q", sp, st)
			}
		}
		if s.Android != nil {
			validateServiceAndroid(sp+".android", *s.Android, e.Android, add)
		}
		if _, ok := profiles[s.AuthProfile]; s.AuthProfile != "" && !ok {
			add("%s.authProfile: auth profile %s is not defined under authProfiles", sp, s.AuthProfile)
		}
	}
	if c := e.Web.Chrome; c != "" && !filepath.IsAbs(c) {
		add("%s.web.chrome must be an absolute path, got %q", p, c)
	}
	if e.Web.Timeout < 0 || e.Web.Timeout > time.Minute {
		add("%s.web.timeout must be a duration from 0 to 1 minute, got %s", p, e.Web.Timeout)
	}
	validateAndroid(p, e, add)
	validateData(p+".data", e.Data, add)
	validateChecks(p+".checks", e.Checks, add)
}

// standaloneAndroid reports whether the environment holds only apps on a device that have no address
// of their own, and no server that needs a host: such an environment has nothing to list in hosts.
func standaloneAndroid(e Environment) bool {
	if len(e.Services) == 0 || e.Android.ServerURL != "" {
		return false
	}
	for _, s := range e.Services {
		if s.BaseURL != "" || s.Android == nil {
			return false
		}
	}
	return true
}

func validateChecks(p string, c Checks, add func(string, ...any)) {
	if !slices.Contains([]string{"", "fail", "warn", "off"}, c.Schema) {
		add("%s.schema must be fail, warn, or off, got %q", p, c.Schema)
	}
	if !slices.Contains([]string{"", "warn", "fail"}, c.ResponseTime.On) {
		add("%s.responseTime.on must be warn or fail, got %q", p, c.ResponseTime.On)
	}
	if c.ResponseTime.Limit < 0 {
		add("%s.responseTime.limit must not be negative", p)
	}
	if !slices.Contains([]string{"", "warn", "off"}, c.Accessibility) {
		add("%s.accessibility must be warn or off, got %q", p, c.Accessibility)
	}
}

const (
	maxDataKeys     = 100
	maxDataKeyBytes = 128
	maxDataValue    = 1024
)

var dataKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)

func validateData(p string, data map[string]string, add func(string, ...any)) {
	if len(data) > maxDataKeys {
		add("%s: at most %d keys are allowed, there are %d", p, maxDataKeys, len(data))
	}
	for _, k := range slices.Sorted(maps.Keys(data)) {
		switch {
		case !dataKeyPattern.MatchString(k):
			add("%s: key %q must match ^[a-z][a-z0-9_]*(\\.[a-z0-9_]+)*$", p, k)
		case len(k) > maxDataKeyBytes:
			add("%s: key %q is longer than %d bytes", p, k, maxDataKeyBytes)
		}
		v := data[k]
		switch {
		case len(v) > maxDataValue:
			add("%s.%s: the value is longer than %d bytes", p, k, maxDataValue)
		case !utf8.ValidString(v):
			add("%s.%s: the value is not valid UTF-8", p, k)
		case strings.ContainsFunc(v, unicode.IsControl):
			add("%s.%s: the value holds a control character", p, k)
		}
	}
}

// originHost returns host[:port] for an error message, with the default port
// of the scheme dropped.
func originHost(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if port == "" || (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		return host
	}
	return host + ":" + port
}

func validatePlan(c Config, add func(string, ...any)) {
	ids := map[string]bool{}
	for i, p := range c.Providers {
		pp := fmt.Sprintf("providers[%d]", i)
		switch err := idcheck.Check("provider id", p.ID); {
		case p.ID == "":
			add("%s.id is required", pp)
		case err != nil:
			add("%s.id: %v", pp, err)
		case ids[p.ID]:
			add("%s.id: %q is used twice", pp, p.ID)
		}
		ids[p.ID] = true
		if p.Kind == "" {
			add("%s.kind is required", pp)
		}
		if p.Model != "" {
			if err := CheckModel(p.Model); err != nil {
				add("%s.model: %v", pp, err)
			}
		}
		for j, o := range p.Outputs {
			if !filepath.IsLocal(o) {
				add("%s.outputs[%d]: %q must be a relative path inside the repository", pp, j, o)
			}
		}
	}
	if b := c.Change.Base; b != "" && (strings.HasPrefix(b, "-") || strings.ContainsAny(b, " ~^:?*[\\\x00") || strings.Contains(b, "..") || strings.HasSuffix(b, "/")) {
		add("change.base: %q is not a branch name", b)
	}
	if _, err := regexp.Compile(c.Tickets.Pattern); err != nil {
		add("tickets.pattern: %v", err)
	}
	// plan.provider may name a provider that only the person's own file defines, so the
	// command line checks that the name is known.
	if c.Plan.Provider != "" {
		if err := idcheck.Check("provider id", c.Plan.Provider); err != nil {
			add("plan.provider: %v", err)
		}
	}
	if c.Plan.MaxScenarios < 0 {
		add("plan.maxScenarios must be positive")
	}
	if c.Plan.MaxEdgeCases != nil && *c.Plan.MaxEdgeCases < 0 {
		add("plan.maxEdgeCases must not be negative")
	}
	if c.Plan.MaxTurns < 0 {
		add("plan.maxTurns must be positive")
	}
	if c.Plan.Timeout < 0 {
		add("plan.timeout must be positive")
	}
	// The cleanup of old copies of the source keeps a copy for 7 days, which is more than the
	// three drafts of a plan can take at this limit.
	if c.Plan.Timeout > MaxPlanTimeout {
		add("plan.timeout must be at most %s", MaxPlanTimeout)
	}
}

// MaxPlanTimeout is the longest time that one planner draft may take.
const MaxPlanTimeout = 24 * time.Hour

const maxDescription = 200

// DescriptionProblem says why an account's description is refused, or "".
func DescriptionProblem(d string) string { return descriptionProblem(d) }

// descriptionProblem says why an account's description is refused, or "".
func descriptionProblem(d string) string {
	switch {
	case !utf8.ValidString(d):
		return "is not valid UTF-8"
	case utf8.RuneCountInString(d) > maxDescription:
		return fmt.Sprintf("is longer than %d characters", maxDescription)
	case strings.ContainsFunc(d, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029'
	}):
		return "holds a control or format character; use one plain line"
	}
	return ""
}

var (
	androidPackagePattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
	androidActivityPattern   = regexp.MustCompile(`^\.?[A-Za-z0-9_.$]+$`)
	androidDevicePattern     = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	androidSchemePattern     = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)
	androidPermissionPattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
	androidAppPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.:/_@%~?=&-]{0,511}$`)
	androidLocalePattern     = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,4}$`)
)

// deniedSchemes can never be the scheme of an open: they run code or read files.
var deniedSchemes = []string{"http", "https", "file", "content", "intent", "javascript"}

// IsLoopbackHost reports whether a host name or address is the machine itself.
func IsLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// Remote reports whether the environment reaches the device through a server at serverUrl.
func (a Android) Remote() bool { return a.ServerURL != "" }

// RemoteLoopback reports whether the server at serverUrl is on this machine.
func (a Android) RemoteLoopback() bool {
	u, err := url.Parse(a.ServerURL)
	return err == nil && IsLoopbackHost(u.Hostname())
}

func validateAndroid(p string, e Environment, add func(string, ...any)) {
	a := e.Android
	p += ".android"
	if a.Device != "" && !androidDevicePattern.MatchString(a.Device) {
		add("%s.device %q must hold letters, digits, and . _ : - only", p, a.Device)
	}
	var u *url.URL
	if a.ServerURL != "" {
		var err error
		u, err = url.Parse(a.ServerURL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
			add("%s.serverUrl must be an absolute http or https URL", p)
			u = nil
		case u.User != nil:
			add("%s.serverUrl must hold no user name or password: use serverUser and serverKey", p)
		case u.RawQuery != "" || u.Fragment != "":
			add("%s.serverUrl must hold no query and no fragment", p)
		case !hostrule.Allowed(u, e.Hosts):
			add("%s: serverUrl host %s is not in hosts", p, originHost(u))
		}
	}
	if (a.ServerUser == "") != (a.ServerKey == "") {
		add("%s needs both serverUser and serverKey, or neither", p)
	}
	for field, ref := range map[string]string{"serverUser": a.ServerUser, "serverKey": a.ServerKey} {
		if ref != "" && !webPasswordPattern.MatchString(ref) {
			add("%s.%s must be a reference to an environment variable, env:NAME, and never the value itself", p, field)
		}
	}
	if a.ServerUser != "" && a.ServerURL == "" {
		add("%s.serverUser needs serverUrl", p)
	}
	if a.ServerUser != "" && u != nil && u.Scheme != "https" && !IsLoopbackHost(u.Hostname()) {
		add("%s: serverUser and serverKey go only to an https serverUrl, or to one on this machine", p)
	}
	if len(a.Capabilities) > 0 {
		if a.ServerURL == "" {
			add("%s.capabilities needs serverUrl", p)
		}
		validateCapabilities(p+".capabilities", a.Capabilities, add)
	}
	if (a.UIAutomator2ServerAPK == "") != (a.UIAutomator2ServerTestAPK == "") {
		add("%s needs both uiautomator2ServerApk and uiautomator2ServerTestApk, or neither", p)
	}
	for field, f := range map[string]string{"uiautomator2ServerApk": a.UIAutomator2ServerAPK, "uiautomator2ServerTestApk": a.UIAutomator2ServerTestAPK} {
		if f == "" {
			continue
		}
		if !filepath.IsLocal(f) && !filepath.IsAbs(f) || strings.ContainsFunc(f, unicode.IsControl) {
			add("%s.%s must be a path inside the repository or an absolute path, got %q", p, field, f)
		}
		if a.ServerURL != "" {
			add("%s.%s is used with adb only, and serverUrl does not use adb", p, field)
		}
	}
	if a.Timeout < 0 || a.Timeout > time.Minute {
		add("%s.timeout must be a duration from 0 to 1 minute, got %s", p, a.Timeout)
	}
	if a.StartTimeout < 0 || a.StartTimeout > 5*time.Minute {
		add("%s.startTimeout must be a duration from 0 to 5 minutes, got %s", p, a.StartTimeout)
	}
	if a.Theme != "" && a.Theme != "light" && a.Theme != "dark" {
		add("%s.theme must be light or dark, got %q", p, a.Theme)
	}
	if a.FontScale != 0 {
		if math.IsNaN(a.FontScale) || math.IsInf(a.FontScale, 0) || a.FontScale < AndroidMinFontScale || a.FontScale > AndroidMaxFontScale {
			add("%s.fontScale must be from %.1f to %.1f, got %g", p, AndroidMinFontScale, AndroidMaxFontScale, a.FontScale)
		}
	}
	if a.Locale != "" && !ValidAndroidLocale(a.Locale) {
		add("%s.locale %q must be a BCP-47 locale such as en-US", p, a.Locale)
	}
	if a.ServerURL != "" {
		if a.Theme != "" {
			add("%s.theme is used with adb only, and serverUrl does not use adb", p)
		}
		if a.FontScale != 0 {
			add("%s.fontScale is used with adb only, and serverUrl does not use adb", p)
		}
		if a.Locale != "" {
			add("%s.locale is used with adb only, and serverUrl does not use adb", p)
		}
	}
}

// maxCapabilities bounds the capabilities of a session, so a file cannot send a body of any size.
const maxCapabilities = 64

// reservedCapabilities are set by Semar and cannot be given in a file.
var reservedCapabilities = []string{"platformName", "appium:platformName", "appium:appPackage", "appium:appActivity", "appium:app", "appium:automationName"}

func validateCapabilities(p string, caps map[string]any, add func(string, ...any)) {
	if len(caps) > maxCapabilities {
		add("%s: at most %d capabilities are allowed", p, maxCapabilities)
	}
	for _, k := range slices.Sorted(maps.Keys(caps)) {
		switch {
		case k == "" || strings.ContainsFunc(k, unicode.IsControl):
			add("%s: a capability name is empty or holds a control character", p)
		case slices.Contains(reservedCapabilities, k):
			add("%s.%s is set by Semar and cannot be given here", p, k)
		}
		if problem := capabilityProblem(caps[k], 0); problem != "" {
			add("%s.%s %s", p, k, problem)
		}
	}
}

func capabilityProblem(v any, depth int) string {
	switch t := v.(type) {
	case string, bool, int, int64, uint64, float64:
		return ""
	case map[string]any:
		if depth >= 3 {
			return "is nested too deep"
		}
		for _, k := range slices.Sorted(maps.Keys(t)) {
			if problem := capabilityProblem(t[k], depth+1); problem != "" {
				return problem
			}
		}
		return ""
	}
	return "must be a string, a number, a boolean, or a mapping of them"
}

func validateServiceAndroid(p string, a ServiceAndroid, env Android, add func(string, ...any)) {
	if !androidPackagePattern.MatchString(a.AppPackage) {
		add("%s.appPackage is required and must be a package name such as com.example.shop, got %q", p, a.AppPackage)
	}
	if a.AppActivity != "" && !androidActivityPattern.MatchString(a.AppActivity) {
		add("%s.appActivity %q must be an activity name such as .MainActivity", p, a.AppActivity)
	}
	remoteFar := env.Remote() && !env.RemoteLoopback()
	if a.APK != "" {
		if !filepath.IsLocal(a.APK) && !filepath.IsAbs(a.APK) || strings.ContainsFunc(a.APK, unicode.IsControl) {
			add("%s.apk must be a path inside the repository or an absolute path, got %q", p, a.APK)
		}
		if remoteFar {
			add("%s.apk cannot be used with a serverUrl that is not on this machine: the server cannot read the file", p)
		}
	}
	if a.App != "" {
		switch {
		case !androidAppPattern.MatchString(a.App):
			add("%s.app %q is not a reference that a server can install", p, a.App)
		case !remoteFar:
			add("%s.app is used only with a serverUrl that is not on this machine", p)
		}
		if a.APK != "" {
			add("%s takes apk or app, and not both", p)
		}
	}
	if !slices.Contains([]string{"", ResetRestart, ResetClear, ResetNone}, a.Reset) {
		add("%s.reset must be %s, %s, or %s, got %q", p, ResetRestart, ResetClear, ResetNone, a.Reset)
	}
	seen := map[string]bool{}
	for i, sc := range a.Schemes {
		switch {
		case !androidSchemePattern.MatchString(sc):
			add("%s.schemes[%d] %q must be a lower-case scheme such as shop", p, i, sc)
		case slices.Contains(deniedSchemes, sc):
			add("%s.schemes[%d]: the scheme %s cannot be listed", p, i, sc)
		case seen[sc]:
			add("%s.schemes[%d]: %s is listed twice", p, i, sc)
		}
		seen[sc] = true
	}
	seen = map[string]bool{}
	for i, perm := range a.Permissions {
		switch {
		case !androidPermissionPattern.MatchString(perm):
			add("%s.permissions[%d] %q must be a permission name such as android.permission.CAMERA", p, i, perm)
		case seen[perm]:
			add("%s.permissions[%d]: %s is listed twice", p, i, perm)
		}
		seen[perm] = true
	}
}

// ValidAndroidPackage reports whether s is a package name such as com.example.shop. A caller that reads
// one from a device checks it with this before it writes it anywhere.
func ValidAndroidPackage(s string) bool { return androidPackagePattern.MatchString(s) }

// ValidAndroidSerial reports whether s is a serial that android.device takes.
func ValidAndroidSerial(s string) bool { return androidDevicePattern.MatchString(s) }

// ValidAndroidLocale reports whether s is a BCP-47 language tag of the simple kind that Android takes.
func ValidAndroidLocale(s string) bool { return len(s) <= 35 && androidLocalePattern.MatchString(s) }
