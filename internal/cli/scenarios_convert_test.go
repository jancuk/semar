package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func legacyYAML(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "legacy", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// toLegacy puts the scenario of the repository back in the YAML format that it had before Gherkin.
func (r *repo) toLegacy(t *testing.T) (yamlFile, featureFile string) {
	t.Helper()
	featureFile = r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")
	yamlFile = r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml")
	if err := os.Remove(featureFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(yamlFile, []byte(legacyYAML(t, scenarioID+".yaml")), 0o600); err != nil {
		t.Fatal(err)
	}
	return yamlFile, featureFile
}

func TestScenariosConvertKeepsEveryConfirmationAndScript(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	before, err := scenario.Load(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature"))
	if err != nil {
		t.Fatal(err)
	}
	yamlFile, featureFile := r.toLegacy(t)

	// Until it is converted, the YAML file is an error and not a scenario that is skipped.
	res := r.must(t, cli.ExitError, "run", "--all")
	contains(t, "stderr", res.stderr, "scenario files are Gherkin now: run semar scenarios convert", yamlFile)

	res = r.must(t, cli.ExitOK, "scenarios", "convert")
	hash := string(domain.ExpectHash(before))
	contains(t, "stdout", res.stdout,
		"converted .semar/scenarios/SHOP-482/"+scenarioID+".yaml to .semar/scenarios/SHOP-482/"+scenarioID+".feature",
		"expectation hash "+hash+" is the same", "converted 1 scenario files to Gherkin")
	if exists(t, yamlFile) {
		t.Error("the YAML file was not deleted")
	}
	if info, err := os.Stat(featureFile); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("the Gherkin file: mode %v, %v; want 0644", info.Mode().Perm(), err)
	}
	after, err := scenario.Load(featureFile)
	if err != nil {
		t.Fatal(err)
	}
	if domain.ExpectHash(after) != domain.ExpectHash(before) {
		t.Error("the expectation hash changed")
	}

	// The confirmation and the stored script of the YAML scenario still hold: the run is not "not confirmed".
	res = r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "stdout", res.stdout, scenarioID+"  passed")
	if strings.Contains(res.stdout+r.lastReport(t), "not confirmed") {
		t.Error("the converted scenario lost its confirmation")
	}
	// A person who confirms it again confirms the same expectation hash.
	res = r.must(t, cli.ExitOK, "confirm", scenarioID, "--yes")
	contains(t, "stdout", res.stdout, "confirmed scenario "+scenarioID+" for expectation hash "+hash)
}

func TestScenariosConvertWritesTheFileInPlaceAndRepeatsNothing(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.toLegacy(t)
	r.must(t, cli.ExitOK, "scenarios", "convert")
	res := r.must(t, cli.ExitOK, "scenarios", "convert")
	contains(t, "stdout", res.stdout, "no YAML scenario file", "nothing to convert")
}

func TestScenariosConvertRefusesToOverwriteAFeatureFile(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	yamlFile := r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml")
	other := r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-09.yaml")
	for _, p := range []string{yamlFile, other} {
		if err := os.WriteFile(p, []byte(strings.Replace(legacyYAML(t, scenarioID+".yaml"), scenarioID, strings.TrimSuffix(filepath.Base(p), ".yaml"), 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	featureFile := r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")
	want, err := os.ReadFile(featureFile)
	if err != nil {
		t.Fatal(err)
	}
	res := r.must(t, cli.ExitError, "scenarios", "convert")
	contains(t, "stderr", res.stderr, ".semar/scenarios/SHOP-482/"+scenarioID+".feature exists already", "does not overwrite", "nothing was changed")
	if got, _ := os.ReadFile(featureFile); string(got) != string(want) {
		t.Error("the Gherkin file was overwritten")
	}
	if !exists(t, yamlFile) || !exists(t, other) {
		t.Error("a YAML file was deleted")
	}
	if exists(t, r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-09.feature")) {
		t.Error("a file was converted although another could not be: convert writes all or none")
	}
}

func TestScenariosConvertRefusesWhatGherkinCannotHold(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	text := legacyYAML(t, scenarioID+".yaml")
	cases := []struct {
		name, text string
		want       []string
	}{
		{"an intent with two line breaks at the end", strings.Replace(strings.Replace(text, "intent: >\n", "intent: >+\n", 1), "unchanged.\n", "unchanged.\n\n", 1), []string{"intent", "more than one line break", "expectation hash", "semar scenarios convert again"}},
		{"a file name that is not the ID", strings.Replace(text, "id: "+scenarioID, "id: SHOP-482-77", 1), []string{`it holds scenario "SHOP-482-77", and its file name says ` + scenarioID}},
		{"a file that is not a scenario", "nothing: here\n", []string{"nothing"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r.toLegacy(t)
			yamlFile := r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml")
			if err := os.WriteFile(yamlFile, []byte(tt.text), 0o600); err != nil {
				t.Fatal(err)
			}
			res := r.must(t, cli.ExitError, "scenarios", "convert")
			contains(t, "stderr", res.stderr, append([]string{"1 of 1 scenario files cannot be converted", "nothing was changed", scenarioID + ".yaml"}, tt.want...)...)
			if !exists(t, yamlFile) || exists(t, r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")) {
				t.Error("convert changed the files")
			}
			if err := os.Remove(yamlFile); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScenariosConvertReadsYMLAndNestedDirectories(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	if err := os.Remove(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature")); err != nil {
		t.Fatal(err)
	}
	nested := r.path(".semar", "scenarios", "team", "refunds", scenarioID+".yml")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte(legacyYAML(t, scenarioID+".yaml")), 0o600); err != nil {
		t.Fatal(err)
	}
	res := r.must(t, cli.ExitOK, "scenarios", "convert")
	contains(t, "stdout", res.stdout, ".semar/scenarios/team/refunds/"+scenarioID+".yml to .semar/scenarios/team/refunds/"+scenarioID+".feature")
	if !exists(t, strings.TrimSuffix(nested, ".yml")+".feature") || exists(t, nested) {
		t.Error("the file was not converted in place")
	}
}

func TestScenariosConvertWithNoScenarioDirectory(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	if err := os.RemoveAll(r.path(".semar", "scenarios")); err != nil {
		t.Fatal(err)
	}
	res := r.must(t, cli.ExitOK, "scenarios", "convert")
	contains(t, "stdout", res.stdout, "nothing to convert")
}

func TestScenariosConvertRefusesALinkedScenario(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	link := r.path(".semar", "scenarios", "SHOP-482", "LINK-1.yaml")
	if err := os.Symlink(filepath.Join(t.TempDir(), "x.yaml"), link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	res := r.must(t, cli.ExitError, "scenarios", "convert")
	contains(t, "stderr", res.stderr, "is a symbolic link", link)
}
