package android_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

func sample(t *testing.T, name string) *mobile.Tree {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := android.ParseSource(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return tree
}

func one(t *testing.T, tree *mobile.Tree, loc domain.Locator) *mobile.Node {
	t.Helper()
	got, err := mobile.Find(tree, loc, nil)
	if err != nil {
		t.Fatalf("%v: %v", loc, err)
	}
	if len(got) != 1 {
		t.Fatalf("%v matched %d nodes", loc, len(got))
	}
	return got[0]
}

func TestViewsSource(t *testing.T) {
	tree := sample(t, "views.xml")
	if tree.Width != 1080 || tree.Height != 2400 || tree.Digest == "" {
		t.Errorf("tree = %dx%d, digest %q", tree.Width, tree.Height, tree.Digest)
	}
	title := one(t, tree, domain.Locator{ID: "title"})
	if title.Text != "Sign in" || title.Role != "heading" || !title.Visible {
		t.Errorf("title = %+v", title)
	}
	email := one(t, tree, domain.Locator{ID: "email"})
	if email.Role != "textbox" || email.Text != "" || email.Hint != "Email address" || !email.Enabled {
		t.Errorf("email = %+v", email)
	}
	pw := one(t, tree, domain.Locator{ID: "password"})
	if pw.Text != "" || !pw.Password || pw.Attrs["text"] != "" || strings.Contains(pw.Desc+pw.Hint+pw.Attrs["text"], "hunter2") {
		t.Errorf("the password field shows %+v", pw)
	}
	remember := one(t, tree, domain.Locator{Role: "checkbox"})
	if !remember.Checked || remember.Text != "Remember me" {
		t.Errorf("remember = %+v", remember)
	}
	submit := one(t, tree, domain.Locator{Role: "button", Name: "Sign in"})
	if submit.Enabled || submit.ID != "com.example.shop:id/submit" {
		t.Errorf("submit = %+v", submit)
	}
	// The title and the button show the same text and are two nodes: a text locator for "Sign in"
	// finds both, and the role of each tells them apart.
	if got, _ := mobile.Find(tree, domain.Locator{Text: "Sign in"}, nil); len(got) != 2 {
		t.Errorf("text Sign in matched %d", len(got))
	}
	help := one(t, tree, domain.Locator{Desc: "Help"})
	if help.Role != "button" || help.Class != "android.widget.ImageButton" {
		t.Errorf("help = %+v", help)
	}
	rows, _ := mobile.Find(tree, domain.Locator{Class: "android.widget.TextView"}, nil)
	var shown []string
	for _, r := range rows {
		if r.Visible && strings.HasPrefix(r.Text, "Row") {
			shown = append(shown, r.Text)
		}
	}
	if len(shown) != 1 || shown[0] != "Row 1 & <more>" {
		t.Errorf("visible rows = %q (entities are decoded, and a node that is not displayed is not visible)", shown)
	}
	if list := one(t, tree, domain.Locator{Role: "list"}); !list.Scrollable || list.Role != "list" {
		t.Errorf("list = %+v", list)
	}
	// The native attributes are there for attribute checks and xpath, and empty ones are not.
	if submit.Attrs["clickable"] != "true" || submit.Attrs["bounds"] != "[48,760][1032,900]" {
		t.Errorf("attrs = %v", submit.Attrs)
	}
	if _, has := submit.Attrs["content-desc"]; has {
		t.Error("an empty attribute is kept")
	}
	x := one(t, tree, domain.Locator{XPath: "//android.widget.Button[@enabled='false' and @text='Sign in']"})
	if x != submit {
		t.Error("xpath did not find the button")
	}
}

func TestComposeSource(t *testing.T) {
	tree := sample(t, "compose.xml")
	pay := one(t, tree, domain.Locator{ID: "pay"})
	if pay.Text != "Pay 12.50" || pay.Role != "button" {
		t.Errorf("pay = %+v", pay)
	}
	remove := one(t, tree, domain.Locator{Desc: "Remove item"})
	if remove.Class != "android.view.View" || remove.Role != "" || remove.Text != "Remove item" {
		t.Errorf("remove = %+v", remove)
	}
	// The total is a view with a description that holds a text: the text is what a person reads.
	total := one(t, tree, domain.Locator{Desc: "Order total"})
	if total.Text != "12.50" {
		t.Errorf("total text = %q", total.Text)
	}
	if got := one(t, tree, domain.Locator{Role: "button", Name: "Pay 12.50"}); got != pay {
		t.Error("the button is not found by its name")
	}
}

func TestReactNativeSource(t *testing.T) {
	tree := sample(t, "reactnative.xml")
	menu := one(t, tree, domain.Locator{TestID: "menu-button"})
	if menu.Desc != "Open menu" || menu.Class != "android.view.ViewGroup" {
		t.Errorf("menu = %+v", menu)
	}
	add := one(t, tree, domain.Locator{ID: "add-to-cart"})
	if add.Text != "Add to cart" {
		t.Errorf("add text = %q", add.Text)
	}
	// The text is in the label inside the button, and is also the description of the button: the
	// innermost node holds the text.
	if got := one(t, tree, domain.Locator{Text: "Add to cart"}); got.Class != "android.widget.TextView" {
		t.Errorf("text locator found %s", got.Class)
	}
	search := one(t, tree, domain.Locator{ID: "search-input"})
	if search.Role != "textbox" || search.Text != "" {
		t.Errorf("search = %+v", search)
	}
	if list := one(t, tree, domain.Locator{ID: "product-list"}); !list.Scrollable {
		t.Errorf("list = %+v", list)
	}
}

func TestFlutterSource(t *testing.T) {
	tree := sample(t, "flutter.xml")
	// Flutter keeps its labels in content-desc.
	welcome := one(t, tree, domain.Locator{Desc: "Welcome back, Ana"})
	if welcome.Text != "Welcome back, Ana" || welcome.Class != "android.view.View" {
		t.Errorf("welcome = %+v", welcome)
	}
	cont := one(t, tree, domain.Locator{Role: "button", Name: "Continue"})
	if cont.Text != "Continue" || cont.ID != "" {
		t.Errorf("continue = %+v", cont)
	}
	email := one(t, tree, domain.Locator{Role: "textbox", Name: "Email"})
	if email.Desc != "Email" {
		t.Errorf("email = %+v", email)
	}
}

func TestParseSourceRefusesWhatIsNotASource(t *testing.T) {
	for name, src := range map[string]string{
		"empty":         "",
		"text":          "hello",
		"json":          `{"value":"x"}`,
		"only a prolog": `<?xml version='1.0'?>`,
		"two roots":     `<a/><b/>`,
	} {
		if _, err := android.ParseSource([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseSourceLimits(t *testing.T) {
	if _, err := android.ParseSource([]byte(strings.Repeat("a", 8<<20+1))); err == nil || !strings.Contains(err.Error(), "larger than 8 MiB") {
		t.Errorf("a source over 8 MiB: %v", err)
	}
	many := "<hierarchy>" + strings.Repeat(`<a bounds="[0,0][1,1]"/>`, 20000) + "</hierarchy>"
	if _, err := android.ParseSource([]byte(many)); err == nil || !strings.Contains(err.Error(), "more than 20000 elements") {
		t.Errorf("20001 elements: %v", err)
	}
	exact := "<hierarchy>" + strings.Repeat(`<a bounds="[0,0][1,1]"/>`, 19999) + "</hierarchy>"
	if tree, err := android.ParseSource([]byte(exact)); err != nil || tree.Count() != 20000 {
		t.Errorf("20000 elements: %v", err)
	}
	deep := strings.Repeat("<a>", 201) + strings.Repeat("</a>", 201)
	if _, err := android.ParseSource([]byte(deep)); err == nil || !strings.Contains(err.Error(), "nested more than 200") {
		t.Errorf("201 levels: %v", err)
	}
	ok := strings.Repeat("<a>", 200) + strings.Repeat("</a>", 200)
	if _, err := android.ParseSource([]byte(ok)); err != nil {
		t.Errorf("200 levels: %v", err)
	}
	long := `<hierarchy><a text="` + strings.Repeat("x", 100000) + `"/></hierarchy>`
	tree, err := android.ParseSource([]byte(long))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(tree.Root.Children[0].Text); got > 4096 {
		t.Errorf("an attribute of %d bytes is kept whole", got)
	}
}

func TestParseSourceReadsATruncatedSourceAsFarAsItGoes(t *testing.T) {
	src := mobiletest.Hierarchy(mobiletest.Widget("android.widget.Button", map[string]string{"text": "A"}, "[0,0][100,100]"))
	cut := src[:len(src)-len("</hierarchy>")]
	tree, err := android.ParseSource([]byte(cut))
	if err != nil || tree.Count() < 2 {
		t.Errorf("tree %v, error %v", tree, err)
	}
}

func TestParseSourceTakesTheSizeFromTheBoundsWhenTheHierarchyHasNone(t *testing.T) {
	src := `<hierarchy><android.widget.FrameLayout class="android.widget.FrameLayout" bounds="[0,0][720,1280]"/></hierarchy>`
	tree, err := android.ParseSource([]byte(src))
	if err != nil || tree.Width != 720 || tree.Height != 1280 {
		t.Errorf("tree = %+v, %v", tree, err)
	}
}

func TestEqualBytesGiveEqualDigests(t *testing.T) {
	a := mobiletest.Hierarchy(mobiletest.Widget("android.widget.Button", map[string]string{"text": "A"}, "[0,0][100,100]"))
	b := strings.Replace(a, `text="A"`, `text="B"`, 1)
	ta, _ := android.ParseSource([]byte(a))
	ta2, _ := android.ParseSource([]byte(a))
	tb, _ := android.ParseSource([]byte(b))
	if ta.Digest != ta2.Digest || ta.Digest == tb.Digest {
		t.Error("digests")
	}
}

func TestRoleTable(t *testing.T) {
	for class, want := range map[string]string{
		"android.widget.Button": "button", "android.widget.ImageButton": "button", "com.google.android.material.button.MaterialButton": "button",
		"androidx.appcompat.widget.AppCompatButton": "button",
		"android.widget.EditText":                   "textbox", "com.google.android.material.textfield.TextInputEditText": "textbox", "android.widget.AutoCompleteTextView": "textbox",
		"android.widget.CheckBox": "checkbox", "android.widget.Switch": "switch", "androidx.appcompat.widget.SwitchCompat": "switch", "android.widget.ToggleButton": "switch",
		"android.widget.RadioButton": "radio", "android.widget.ImageView": "img", "android.widget.TextView": "text", "android.widget.CheckedTextView": "text",
		"android.widget.Spinner": "combobox", "android.widget.SeekBar": "slider", "android.widget.ProgressBar": "progressbar",
		"androidx.recyclerview.widget.RecyclerView": "list", "android.widget.ListView": "list", "android.webkit.WebView": "document",
		"android.view.View": "", "android.widget.LinearLayout": "", "hierarchy": "",
	} {
		src := `<hierarchy><` + class + ` class="` + class + `" bounds="[0,0][1,1]"/></hierarchy>`
		tree, err := android.ParseSource([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		if got := tree.Root.Children[0].Role; got != want {
			t.Errorf("%s: role %q, want %q", class, got, want)
		}
	}
	h, _ := android.ParseSource([]byte(`<hierarchy><android.widget.TextView heading="true" bounds="[0,0][1,1]"/></hierarchy>`))
	if h.Root.Children[0].Role != "heading" {
		t.Error("a heading")
	}
}
