package config_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
)

const editable = `# The test environment of the orders application.
version: 1
environments:
  uat:
    kind: test
    hosts: [api.uat.example.com]
    methods: [GET, POST]
    effects: {mode: test_data}
    services:
      - {name: orders, baseUrl: "https://api.uat.example.com"}
authProfiles:
  # The account of a customer.
  default:
    description: A customer
    session: {header: Authorization, value: "Bearer {{secret.TOKEN}}"}
    secrets: {TOKEN: "env:ORDERS_TOKEN"}  # kept in the environment

  support:
    session: {header: Authorization, value: "Bearer {{secret.TOKEN}}"}
    secrets: {TOKEN: "env:ORDERS_TOKEN_SUPPORT"}
redact:
  headers: [Authorization]
`

func TestSetWebSignInAddsOneLineAndKeepsTheRest(t *testing.T) {
	got, err := config.SetWebSignIn([]byte(editable), "default", config.WebSignIn{User: "ana@example.com", Password: "env:ORDERS_WEB_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	added := `    web: {user: ana@example.com, password: "env:ORDERS_WEB_PASSWORD"}` + "\n"
	want := strings.Replace(editable, "# kept in the environment\n", "# kept in the environment\n"+added, 1)
	if string(got) != want {
		t.Fatalf("the file changed more than the one line:\n%s", got)
	}
	cfg, err := loadText(t, string(got))
	if err != nil {
		t.Fatal(err)
	}
	if w := cfg.AuthProfiles["default"].Web; w == nil || w.User != "ana@example.com" || w.PasswordEnv() != "ORDERS_WEB_PASSWORD" {
		t.Errorf("web = %+v", w)
	}
	if cfg.AuthProfiles["support"].Web != nil {
		t.Error("another account gained the block")
	}
}

func TestSetWebSignInReplacesTheBlockThatThereIs(t *testing.T) {
	once, err := config.SetWebSignIn([]byte(editable), "support", config.WebSignIn{User: "sue@example.com", Password: "env:OLD"})
	if err != nil {
		t.Fatal(err)
	}
	twice, err := config.SetWebSignIn(once, "support", config.WebSignIn{User: "sue@example.com", Password: "env:NEW"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(twice), "web:") != 1 || !strings.Contains(string(twice), `password: "env:NEW"`) || strings.Contains(string(twice), "OLD") {
		t.Fatalf("the block was not replaced:\n%s", twice)
	}
	again, err := config.SetWebSignIn(twice, "support", config.WebSignIn{User: "sue@example.com", Password: "env:NEW"})
	if err != nil || string(again) != string(twice) {
		t.Errorf("the same block written again changed the file: %v", err)
	}
}

func TestSetWebSignInRefusesWhatItCannotPlace(t *testing.T) {
	w := config.WebSignIn{User: "ana", Password: "env:P"}
	for name, c := range map[string]struct{ account, text, want string }{
		"an unknown account": {"nobody", editable, "nobody"},
		"a flow mapping":     {"default", strings.Replace(editable, "  default:\n    description: A customer\n    session: {header: Authorization, value: \"Bearer {{secret.TOKEN}}\"}\n    secrets: {TOKEN: \"env:ORDERS_TOKEN\"}  # kept in the environment\n", "  default: {secrets: {TOKEN: \"env:ORDERS_TOKEN\"}}\n", 1), "by hand"},
		"a bad password":     {"default", editable, "password"},
	} {
		in := w
		if name == "a bad password" {
			in.Password = "hunter2"
		}
		_, err := config.SetWebSignIn([]byte(c.text), c.account, in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", name, c.want, err)
		}
	}
}
