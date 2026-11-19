package config_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
)

const blockHosts = `# The test environment of the shop.
version: 1
environments:
  uat:
    kind: test
    hosts:
      - shop.example.com   # the application
      # the API
      - "api.example.com:8443"
    methods: [GET]
    effects: {mode: test_data}
    services:
      - {name: shop, baseUrl: "https://shop.example.com"}
  dev:
    kind: test
    hosts: ["dev.example.com"]
    methods: [GET]
    effects: {mode: test_data}
    services:
      - {name: shop, baseUrl: "https://dev.example.com"}
authProfiles:
  default: {}
`

func hostsOf(t *testing.T, text, env string) []string {
	t.Helper()
	cfg, err := loadText(t, text)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Environments[env].Hosts
}

func TestAddHostsToTheFlowFormKeepsEveryOtherLine(t *testing.T) {
	got, err := config.AddHosts([]byte(blockHosts), "dev", []string{"cdn.example.com", "127.0.0.1:9000", "[::1]:9001"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(blockHosts, `hosts: ["dev.example.com"]`, `hosts: ["dev.example.com", "cdn.example.com", "127.0.0.1:9000", "[::1]:9001"]`, 1)
	if string(got) != want {
		t.Fatalf("the file changed more than the one line:\n%s", got)
	}
	if h := hostsOf(t, string(got), "dev"); !slices.Equal(h, []string{"dev.example.com", "cdn.example.com", "127.0.0.1:9000", "[::1]:9001"}) {
		t.Errorf("hosts = %v", h)
	}
}

func TestAddHostsToTheFlowFormWithACommentAndWithNoEntry(t *testing.T) {
	commented := strings.Replace(blockHosts, `hosts: ["dev.example.com"]`, `hosts: [dev.example.com]  # the dev shop [one]`, 1)
	got, err := config.AddHosts([]byte(commented), "dev", []string{"cdn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(commented, `[dev.example.com]  #`, `[dev.example.com, cdn.example.com]  #`, 1); string(got) != want {
		t.Errorf("the comment moved or the line changed:\n%s", got)
	}
	empty := strings.Replace(blockHosts, `hosts: ["dev.example.com"]`, `hosts: []`, 1)
	got, err = config.AddHosts([]byte(empty), "dev", []string{"dev.example.com", "cdn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(empty, `hosts: []`, `hosts: [dev.example.com, cdn.example.com]`, 1); string(got) != want {
		t.Errorf("an empty list:\n%s", got)
	}
}

func TestAddHostsToTheBlockFormAddsOneLineForEach(t *testing.T) {
	got, err := config.AddHosts([]byte(blockHosts), "uat", []string{"cdn.example.com", "127.0.0.1:9000"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(blockHosts, "      - \"api.example.com:8443\"\n", "      - \"api.example.com:8443\"\n      - cdn.example.com\n      - 127.0.0.1:9000\n", 1)
	if string(got) != want {
		t.Fatalf("the file changed more than the new lines:\n%s", got)
	}
	if h := hostsOf(t, string(got), "uat"); !slices.Equal(h, []string{"shop.example.com", "api.example.com:8443", "cdn.example.com", "127.0.0.1:9000"}) {
		t.Errorf("hosts = %v", h)
	}
	if h := hostsOf(t, string(got), "dev"); !slices.Equal(h, []string{"dev.example.com"}) {
		t.Errorf("another environment changed: %v", h)
	}
}

func TestAddHostsQuotesWhatYamlWouldReadAsSomethingElse(t *testing.T) {
	got, err := config.AddHosts([]byte(blockHosts), "uat", []string{"[::1]:9001", "123"})
	if err != nil {
		t.Fatal(err)
	}
	if h := hostsOf(t, string(got), "uat"); !slices.Equal(h[2:], []string{"[::1]:9001", "123"}) {
		t.Errorf("hosts = %v\n%s", h, got)
	}
}

func TestAddHostsSkipsWhatIsListedAndChangesNothingWhenThereIsNothingToAdd(t *testing.T) {
	got, err := config.AddHosts([]byte(blockHosts), "uat", []string{"SHOP.example.com", "api.example.com:8443"})
	if err != nil || string(got) != blockHosts {
		t.Errorf("listed hosts changed the file: %v\n%s", err, got)
	}
	once, err := config.AddHosts([]byte(blockHosts), "uat", []string{"cdn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	twice, err := config.AddHosts(once, "uat", []string{"cdn.example.com"})
	if err != nil || string(twice) != string(once) {
		t.Errorf("the same host written twice changed the file: %v", err)
	}
}

func TestAddHostsWritesTheListOfAnEnvironmentThatHasNone(t *testing.T) {
	bare := strings.Replace(blockHosts, "    hosts: [\"dev.example.com\"]\n", "", 1)
	got, err := config.AddHosts([]byte(bare), "dev", []string{"dev.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if h := hostsOf(t, string(got), "dev"); !slices.Equal(h, []string{"dev.example.com"}) {
		t.Errorf("hosts = %v\n%s", h, got)
	}
	if !strings.Contains(string(got), "    hosts:\n      - dev.example.com\n") {
		t.Errorf("the list is not written as a block at the indent of the environment:\n%s", got)
	}
}

func TestAddHostsRefusesWhatItCannotPlace(t *testing.T) {
	multi := strings.Replace(blockHosts, `hosts: ["dev.example.com"]`, "hosts: [\"dev.example.com\",\n      \"b.example.com\"]", 1)
	for name, c := range map[string]struct{ env, text, want string }{
		"an unknown environment":         {"nope", blockHosts, "nope"},
		"a flow list over several lines": {"dev", multi, "by hand"},
		"a bad entry":                    {"dev", blockHosts, "colon"},
	} {
		hosts := []string{"cdn.example.com"}
		if name == "a bad entry" {
			hosts = []string{"cdn.example.com:"}
		}
		_, err := config.AddHosts([]byte(c.text), c.env, hosts)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", name, c.want, err)
		}
	}
}
