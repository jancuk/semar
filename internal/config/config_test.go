package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

func loadText(t *testing.T, text string) (config.Config, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".semar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".semar", "semar.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Load(root)
}

func loadFixture(t *testing.T, name string) (config.Config, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return loadText(t, string(data))
}

func TestEnvironmentKinds(t *testing.T) {
	const rest = "    hosts: [h]\n    methods: [GET]\n    effects: {mode: read_only}\n"
	for _, kind := range []string{"test", "production"} {
		cfg, err := loadText(t, "version: 1\nenvironments:\n  e:\n    kind: "+kind+"\n"+rest)
		if err != nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
		if got := cfg.Environments["e"].Kind; got != kind {
			t.Errorf("kind = %q, want %q", got, kind)
		}
	}
	// A production environment still states its methods and mode.
	_, err := loadText(t, "version: 1\nenvironments:\n  e:\n    kind: production\n    hosts: [h]\n")
	if err == nil || !strings.Contains(err.Error(), "methods needs at least one entry") || !strings.Contains(err.Error(), "effects.mode is required") {
		t.Errorf("want methods and mode to stay required, got %v", err)
	}
	if _, err := loadText(t, "version: 1\nenvironments:\n  e:\n"+rest); err == nil || !strings.Contains(err.Error(), "kind must be test or production") {
		t.Errorf("a missing kind: got %v", err)
	}
}

func TestLoad(t *testing.T) {
	cases := []struct{ file, want string }{
		{"valid.yaml", ""},
		{"unknown-key.yaml", "semar.yaml:4"},
		{"kind-unknown.yaml", "kind must be test or production, got \"staging\""},
		{"no-effects-mode.yaml", "effects.mode is required: test_data or read_only"},
		{"service-off-list.yaml", "baseUrl host api.other.example.com is not in hosts"},
		{"later-plan-keys.yaml", ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			_, err := loadFixture(t, c.file)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestLoadUnknownKeys(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"top level", "version: 1\nenviroments: {}\n", []string{"semar.yaml:2", "enviroments"}},
		{"inside a known block", "version: 1\nreplay:\n  worker: 2\n", []string{"semar.yaml:3", "worker"}},
		{"inside an environment", "version: 1\nenvironments:\n  uat:\n    kindd: test\n", []string{"semar.yaml:4", "kindd"}},
		{"two documents", "version: 1\n---\nversion: 1\n", []string{"semar.yaml", "more than one"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadText(t, c.text)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := config.Load(t.TempDir()); err == nil || !strings.Contains(err.Error(), "semar.yaml") {
		t.Fatalf("want an error that names the file, got %v", err)
	}
}

const envHead = "version: 1\nenvironments:\n  uat:\n    kind: test\n"

func TestLoadValidation(t *testing.T) {
	ok := "    hosts: [api.uat.example.com]\n    methods: [GET]\n    effects: {mode: read_only}\n"
	svc := func(url string) string {
		return envHead + ok + "    services:\n      - {name: api, baseUrl: \"" + url + "\"}\n"
	}
	cases := []struct{ name, text, want string }{
		{"version missing", "environments: {}\n", "version must be 1"},
		{"version 2", "version: 2\n", "version must be 1"},
		{"no hosts", envHead + "    methods: [GET]\n    effects: {mode: read_only}\n", "hosts needs at least one entry"},
		{"host entry colon without port", envHead + "    hosts: [\"api.uat.example.com:\"]\n    methods: [GET]\n    effects: {mode: read_only}\n", "api.uat.example.com:"},
		{"no methods", envHead + "    hosts: [h]\n    effects: {mode: read_only}\n", "methods needs at least one entry"},
		{"bad mode", envHead + "    hosts: [h]\n    methods: [GET]\n    effects: {mode: write}\n", "effects.mode is required: test_data or read_only"},
		{"service no name", envHead + ok + "    services:\n      - {baseUrl: \"https://api.uat.example.com\"}\n", "name is required"},
		{"service name with =", envHead + ok + "    services:\n      - {name: \"api=x\", baseUrl: \"https://api.uat.example.com\"}\n", "services[0].name: invalid service name"},
		{"service name with a space", envHead + ok + "    services:\n      - {name: \"api x\", baseUrl: \"https://api.uat.example.com\"}\n", "services[0].name: invalid service name"},
		{"service name with a shell character", envHead + ok + "    services:\n      - {name: \"api;rm\", baseUrl: \"https://api.uat.example.com\"}\n", "services[0].name: invalid service name"},
		{"service name with a slash", envHead + ok + "    services:\n      - {name: \"../api\", baseUrl: \"https://api.uat.example.com\"}\n", "services[0].name: invalid service name"},
		{"environment name", "version: 1\nenvironments:\n  \"u at\":\n" + ok, `invalid environment name "u at"`},
		{"auth profile name", "version: 1\nauthProfiles:\n  \"a=b\": {}\n", `invalid auth profile name "a=b"`},
		{"relative url", svc("/v1"), "baseUrl must be an absolute http or https URL"},
		{"ftp url", svc("ftp://api.uat.example.com"), "baseUrl must be an absolute http or https URL"},
		{"https default port", svc("https://api.uat.example.com:443"), ""},
		{"host case", svc("https://API.uat.example.com"), ""},
		{"port not in hosts", svc("http://api.uat.example.com:8080"), "baseUrl host api.uat.example.com:8080 is not in hosts"},
		{"loopback needs port entry", strings.Replace(svc("http://127.0.0.1:8080"), "api.uat.example.com", "127.0.0.1", 1), "baseUrl host 127.0.0.1:8080 is not in hosts"},
		{"loopback with port entry", strings.Replace(svc("http://127.0.0.1:8080"), "api.uat.example.com", "127.0.0.1:8080", 1), ""},
		{"bad trust source", "version: 1\npolicy: {trust: {confirm: [diff, invariant]}}\n", "trust.confirm: invariant is not a source"},
		{"negative workers", "version: 1\nreplay: {workers: -1}\n", "replay.workers must not be negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadText(t, c.text)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	cases := []struct {
		name, text string
		confirm    []domain.Source
		workers    int
	}{
		{"nothing set", "version: 1\n", []domain.Source{domain.SourceDiff, domain.SourceBaseline}, 4},
		{"zero workers", "version: 1\nreplay: {workers: 0}\n", []domain.Source{domain.SourceDiff, domain.SourceBaseline}, 4},
		{"explicit", "version: 1\nreplay: {workers: 2}\npolicy: {trust: {confirm: [ticket]}}\n", []domain.Source{domain.SourceTicket}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadText(t, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.confirm, cfg.Policy.Trust.Confirm) {
				t.Fatalf("confirm = %v, want %v", cfg.Policy.Trust.Confirm, c.confirm)
			}
			if cfg.Replay.Workers != c.workers {
				t.Fatalf("workers = %d, want %d", cfg.Replay.Workers, c.workers)
			}
		})
	}
}

func TestEnvironment(t *testing.T) {
	one := "version: 1\nenvironments:\n  uat: &e\n    kind: test\n    hosts: [h]\n    methods: [GET]\n    effects: {mode: read_only}\n"
	two := one + "  qa: *e\n"
	cases := []struct {
		name, text, ask string
		wantName        string
		wantErr         []string
	}{
		{"only one by empty name", one, "", "uat", nil},
		{"by name", two, "qa", "qa", nil},
		{"several and no name", two, "", "", []string{"qa, uat"}},
		{"unknown name", two, "prod", "", []string{"prod", "qa, uat"}},
		{"unknown name with one", one, "prod", "", []string{"prod", "uat"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadText(t, c.text)
			if err != nil {
				t.Fatal(err)
			}
			name, env, err := cfg.Environment(c.ask)
			if len(c.wantErr) > 0 {
				if err == nil {
					t.Fatal("want an error")
				}
				for _, w := range c.wantErr {
					if !strings.Contains(err.Error(), w) {
						t.Fatalf("error %q lacks %q", err, w)
					}
				}
				return
			}
			if err != nil || name != c.wantName || !slices.Equal(env.Hosts, []string{"h"}) {
				t.Fatalf("got %q %+v %v", name, env, err)
			}
		})
	}
	t.Run("no environments", func(t *testing.T) {
		cfg, err := loadText(t, "version: 1\n")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := cfg.Environment(""); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestPlanBlocks(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := loadText(t, "version: 1\n")
		if err != nil {
			t.Fatal(err)
		}
		want := []config.Provider{{ID: "claude", Kind: "claude"}}
		if !reflect.DeepEqual(cfg.Providers, want) {
			t.Fatalf("providers = %+v", cfg.Providers)
		}
		wantPlan := config.Plan{MaxScenarios: 12, MaxTurns: 60, Timeout: 15 * time.Minute}
		if cfg.Plan != wantPlan || cfg.Tickets.Pattern != "[A-Z][A-Z0-9]+-[0-9]+" {
			t.Fatalf("plan = %+v tickets = %+v", cfg.Plan, cfg.Tickets)
		}
		if cfg.PlanProvider().ID != "claude" {
			t.Fatal("want the first provider")
		}
	})
	t.Run("explicit", func(t *testing.T) {
		cfg, err := loadText(t, `version: 1
providers:
  - {id: claude, kind: claude, model: "claude-sonnet-5-5[1m]"}
  - {id: bedrock, kind: claude, model: "us.anthropic.claude-opus-5-5:0/x_y"}
  - {id: fake, kind: fake, outputs: [a/b.json]}
tickets: {pattern: "X-[0-9]+"}
plan: {provider: fake, maxScenarios: 3, maxTurns: 7, timeout: 90s}
`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PlanProvider().ID != "fake" || cfg.Plan.MaxScenarios != 3 || cfg.Plan.MaxTurns != 7 || cfg.Plan.Timeout != 90*time.Second || cfg.Tickets.Pattern != "X-[0-9]+" {
			t.Fatalf("got %+v", cfg)
		}
	})
	t.Run("a timeout of one day", func(t *testing.T) {
		cfg, err := loadText(t, "version: 1\nplan: {timeout: 24h}\n")
		if err != nil || cfg.Plan.Timeout != 24*time.Hour {
			t.Fatalf("got %+v, %v", cfg.Plan, err)
		}
	})
	cases := []struct{ name, text, want string }{
		{"unknown provider key", "version: 1\nproviders:\n  - id: a\n    kind: claude\n    bogus: 1\n", "semar.yaml:5"},
		{"an address from the repository", "version: 1\nproviders:\n  - id: a\n    kind: openai\n    baseUrl: https://example.com\n", "semar.yaml:5"},
		{"a key from the repository", "version: 1\nproviders:\n  - id: a\n    kind: openai\n    apiKey: env:KEY\n", "semar.yaml:5"},
		{"a reasoning level from the repository", "version: 1\nproviders:\n  - id: a\n    kind: claude\n    reasoning: high\n", "semar.yaml:5"},
		{"a setup from the repository", "version: 1\nproviders:\n  - id: a\n    kind: claude\n    setup: {mcp: true}\n", "semar.yaml:5"},
		{"unknown plan key", "version: 1\nplan:\n  maxTurn: 1\n", "semar.yaml:3"},
		{"unknown tickets key", "version: 1\ntickets:\n  regex: x\n", "semar.yaml:3"},
		{"plan provider is unsafe", "version: 1\nplan: {provider: ../nope}\n", "plan.provider"},
		{"duplicate id", "version: 1\nproviders: [{id: a, kind: x}, {id: a, kind: x}]\n", "used twice"},
		{"unsafe id", "version: 1\nproviders: [{id: ../a, kind: x}]\n", "invalid provider id"},
		{"empty id", "version: 1\nproviders: [{kind: x}]\n", "id is required"},
		{"no kind", "version: 1\nproviders: [{id: a}]\n", "kind is required"},
		{"bad pattern", "version: 1\ntickets: {pattern: \"[\"}\n", "tickets.pattern"},
		{"model like a flag", "version: 1\nproviders: [{id: a, kind: claude, model: \"--dangerously-skip-permissions\"}]\n", "providers[0].model"},
		{"model with a space", "version: 1\nproviders: [{id: a, kind: claude, model: \"m1 --x\"}]\n", "providers[0].model"},
		{"model with a newline", "version: 1\nproviders: [{id: a, kind: claude, model: \"m1\\n\"}]\n", "providers[0].model"},
		{"model too long", "version: 1\nproviders: [{id: a, kind: claude, model: " + strings.Repeat("m", 129) + "}]\n", "providers[0].model"},
		{"negative turns", "version: 1\nplan: {maxTurns: -1}\n", "plan.maxTurns must be positive"},
		{"negative scenarios", "version: 1\nplan: {maxScenarios: -1}\n", "plan.maxScenarios must be positive"},
		{"negative timeout", "version: 1\nplan: {timeout: -1m}\n", "plan.timeout must be positive"},
		{"timeout above a day", "version: 1\nplan: {timeout: 25h}\n", "plan.timeout must be at most 24h"},
		{"bad timeout", "version: 1\nplan: {timeout: soon}\n", "semar.yaml:2"},
		{"output escapes", "version: 1\nproviders: [{id: a, kind: fake, outputs: [../x.json]}]\n", "outputs[0]"},
		{"output absolute", "version: 1\nproviders: [{id: a, kind: fake, outputs: [/x.json]}]\n", "outputs[0]"},
		{
			"binary path", "version: 1\nproviders:\n  - id: a\n    kind: fake\n  - id: b\n    kind: claude\n    binaryPath: ./tools/claude\n",
			"semar.yaml:7: providers[1].binaryPath: the path of a provider binary is not read from the repository's configuration; set the environment variable SEMAR_CLAUDE_BIN to an absolute path instead",
		},
		{
			"binary path through an alias", "version: 1\nx: &p {id: b, kind: claude, binaryPath: /bin/sh}\nproviders: [*p]\n",
			"providers[0].binaryPath",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadText(t, c.text)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}

const envBody = "    hosts: [api.uat.example.com]\n    methods: [GET]\n    effects: {mode: read_only}\n"

func TestServiceAuthProfileMustExist(t *testing.T) {
	services := "    services:\n      - {name: orders, baseUrl: \"https://api.uat.example.com\", authProfile: orders-tester}\n      - {name: payments, baseUrl: \"https://api.uat.example.com\", authProfile: payments-tester}\n"
	profiles := "authProfiles:\n  orders-tester: {}\n"
	cfg, err := loadText(t, envHead+envBody+services+profiles+"  payments-tester: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Environments["uat"].Services
	if svc[0].AuthProfile != "orders-tester" || svc[1].AuthProfile != "payments-tester" {
		t.Fatalf("got %+v", svc)
	}

	_, err = loadText(t, envHead+envBody+services+profiles)
	want := "environments.uat.services[1].authProfile: auth profile payments-tester is not defined under authProfiles"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want an error with %q, got %v", want, err)
	}
	if strings.Contains(err.Error(), "services[0]") {
		t.Fatalf("the profile of services[0] is defined: %v", err)
	}

	// An empty profile is allowed: the scenario's profile is used.
	cfg, err = loadText(t, envHead+envBody+"    services:\n      - {name: orders, baseUrl: \"https://api.uat.example.com\"}\n")
	if err != nil || cfg.Environments["uat"].Services[0].AuthProfile != "" {
		t.Fatalf("got %+v, %v", cfg, err)
	}

	// No authProfiles block at all.
	_, err = loadText(t, envHead+envBody+services)
	if err == nil || !strings.Contains(err.Error(), "services[0].authProfile: auth profile orders-tester is not defined") {
		t.Fatalf("got %v", err)
	}
}

func TestDataKeysAndValues(t *testing.T) {
	many := func(n int) string {
		var b strings.Builder
		b.WriteString("    data:\n")
		for i := range n {
			b.WriteString("      k" + strings.Repeat("a", i%3) + "_" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ": v\n")
		}
		return b.String()
	}
	cases := []struct{ name, data, want string }{
		{"flat keys", "    data:\n      customers.default: c-1\n      region: id\n      plan.tier_2: gold\n", ""},
		{"numbers and booleans are text", "    data:\n      a: 5\n      b: true\n      c: 1.50\n", ""},
		{"a key in upper case", "    data:\n      Customers: x\n", `environments.uat.data: key "Customers" must match ^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`},
		{"a key that starts with a digit", "    data:\n      1a: x\n", `key "1a" must match`},
		{"a key with a hyphen", "    data:\n      a-b: x\n", `key "a-b" must match`},
		{"a key that ends with a dot", "    data:\n      \"a.\": x\n", `key "a." must match`},
		{"a key with an empty part", "    data:\n      \"a..b\": x\n", `key "a..b" must match`},
		{"a key of 128 bytes", "    data:\n      " + strings.Repeat("a", 128) + ": x\n", ""},
		{"a key of 129 bytes", "    data:\n      " + strings.Repeat("a", 129) + ": x\n", "is longer than 128 bytes"},
		{"100 keys", many(100), ""},
		{"101 keys", many(101), "environments.uat.data: at most 100 keys are allowed, there are 101"},
		{"a value of 1024 bytes", "    data:\n      a: " + strings.Repeat("x", 1024) + "\n", ""},
		{"a value of 1025 bytes", "    data:\n      a: " + strings.Repeat("x", 1025) + "\n", "environments.uat.data.a: the value is longer than 1024 bytes"},
		{"a control character", "    data:\n      a: \"x\\x07y\"\n", "environments.uat.data.a: the value holds a control character"},
		{"a line feed", "    data:\n      a: \"x\\ny\"\n", "the value holds a control character"},
		{"an escape", "    data:\n      a: \"x\\ey\"\n", "the value holds a control character"},
		{"a value that is a list", "    data:\n      a: [1, 2]\n", "semar.yaml:"},
		{"a value that is a mapping", "    data:\n      a: {b: 1}\n", "semar.yaml:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadText(t, envHead+envBody+c.data)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(cfg.Environments["uat"].Data) == 0 {
					t.Fatal("the data was not read")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}

	t.Run("values are read as text", func(t *testing.T) {
		cfg, err := loadText(t, envHead+envBody+"    data:\n      customers.default: c-1\n      n: 5\n      f: 1.50\n      b: true\n")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"customers.default": "c-1", "n": "5", "f": "1.50", "b": "true"}
		if !reflect.DeepEqual(cfg.Environments["uat"].Data, want) {
			t.Fatalf("got %v", cfg.Environments["uat"].Data)
		}
	})

	t.Run("a second environment names its own", func(t *testing.T) {
		text := envHead + envBody + "    data: {a: 1}\n  prod:\n    kind: production\n" + envBody + "    data: {Bad: 1}\n"
		_, err := loadText(t, text)
		if err == nil || !strings.Contains(err.Error(), `environments.prod.data: key "Bad"`) || strings.Contains(err.Error(), "environments.uat.data") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLoadFixtureConfiguration(t *testing.T) {
	cfg, err := loadFixture(t, "fixtures.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env := cfg.Environments["uat"]
	if env.Data["customers.default"] != "c-1001" || env.Services[1].AuthProfile != "payments-tester" {
		t.Fatalf("got %+v", env)
	}
}

func TestUnknownKeyUnderServiceStillRefused(t *testing.T) {
	text := envHead + envBody + "    services:\n      - name: orders\n        baseUrl: https://api.uat.example.com\n        authProfil: x\n"
	_, err := loadText(t, text)
	if err == nil || !strings.Contains(err.Error(), "semar.yaml:") || !strings.Contains(err.Error(), "authProfil") {
		t.Fatalf("got %v", err)
	}
	_, err = loadText(t, envHead+envBody+"    dat: {a: b}\n")
	if err == nil || !strings.Contains(err.Error(), "dat") {
		t.Fatalf("an unknown key in an environment: got %v", err)
	}
}

func TestPlanTrialDefaultsOn(t *testing.T) {
	for _, text := range []string{"version: 1\n", "version: 1\nplan: {maxTurns: 7}\n", "version: 1\nplan: {trial: true}\n"} {
		cfg, err := loadText(t, text)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Plan.TrialOn() {
			t.Fatalf("TrialOn is false for %q", text)
		}
	}
	if !(config.Plan{}).TrialOn() {
		t.Fatal("TrialOn is false for the zero Plan")
	}
}

func TestPlanTrialFalse(t *testing.T) {
	cfg, err := loadText(t, "version: 1\nplan:\n  trial: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plan.TrialOn() || cfg.Plan.Trial == nil || *cfg.Plan.Trial {
		t.Fatalf("plan = %+v, want the trial off", cfg.Plan)
	}
	if cfg.Plan.MaxTurns != 60 {
		t.Fatalf("the other plan defaults were lost: %+v", cfg.Plan)
	}
}

func TestPlanTrialNotBoolean(t *testing.T) {
	for _, value := range []string{"maybe", `"false"`, "3", "[true]", "{a: 1}"} {
		t.Run(value, func(t *testing.T) {
			_, err := loadText(t, "version: 1\nplan:\n  trial: "+value+"\n")
			if err == nil {
				t.Fatalf("plan.trial %s was accepted", value)
			}
			if !strings.Contains(err.Error(), "semar.yaml:3") {
				t.Fatalf("error %q does not name the file and the line", err)
			}
		})
	}
}
