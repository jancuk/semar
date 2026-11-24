package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mobilerec"
)

const (
	viaDirect   = "direct"
	viaKeyboard = "keyboard"
	// maxKeyboardRunes bounds a text that is typed key by key: each character costs a read and a tap.
	maxKeyboardRunes = 256
)

// typeVia reads the via argument of mobile_type_keys.
func typeVia(s string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "", viaDirect:
		return viaDirect, nil
	case viaKeyboard:
		return viaKeyboard, nil
	default:
		return "", fmt.Errorf("via is %q: use direct or keyboard", truncateText(s, 20))
	}
}

type keyboardArgs struct {
	Text      *string
	Submit    bool
	Ref       string
	SecretEnv string
	Replace   bool
}

// typeViaKeyboard types a text by tapping, for each character, the one key of the keyboard window that
// shows it. The tree is read again after each tap, because a tap changes the screen: the keys may change
// (shift), and a preview popup may show the key twice.
func (m *Manager) typeViaKeyboard(ctx context.Context, a keyboardArgs) (mcp.Result, error) {
	switch {
	case a.SecretEnv != "":
		// The key that is tapped is named in the result and the log, so the labels would spell the secret.
		return mcp.Result{}, errors.New("via: keyboard is refused with secretEnv: each key that is tapped is shown, so the secret would be too. Type a secret with via: direct")
	case a.Text == nil:
		return mcp.Result{}, errors.New("give the text to type")
	case a.Replace:
		return mcp.Result{}, errors.New("replace is not available with via: keyboard, which only taps keys. Clear the field with via: direct and an empty text first, or tap the delete key")
	}
	runes := []rune(*a.Text)
	if len(runes) > maxKeyboardRunes {
		return mcp.Result{}, fmt.Errorf("via: keyboard types at most %d characters, and the text has %d. Use via: direct for a long text", maxKeyboardRunes, len(runes))
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	where := "the focused field"
	if strings.TrimSpace(a.Ref) != "" {
		t, err := m.resolveRef(ctx, dev, a.Ref)
		if err != nil {
			return mcp.Result{}, err
		}
		if err := dev.Touch(ctx, mobile.Gesture{From: t.Point, To: t.Point}); err != nil {
			return mcp.Result{}, err
		}
		where = t.Ref
	}
	pkg := ""
	for i, r := range runes {
		if err := ctx.Err(); err != nil {
			return mcp.Result{}, err
		}
		tree, err := dev.Source(ctx)
		if err != nil {
			return mcp.Result{}, err
		}
		key, kpkg, err := KeyFor(tree, m.hints(ctx, dev, tree), r)
		if err != nil {
			return mcp.Result{}, fmt.Errorf("%w. %d of %d characters were typed before it", err, i, len(runes))
		}
		pkg = kpkg
		c := key.Bounds.Center()
		if err := dev.Touch(ctx, mobile.Gesture{From: c, To: c}); err != nil {
			return mcp.Result{}, fmt.Errorf("%d of %d characters were typed, then a key could not be tapped: %w", i, len(runes), err)
		}
	}
	if a.Submit {
		code, _ := android.ButtonKeycode("ENTER")
		if err := dev.Key(ctx, code); err != nil {
			return mcp.Result{}, fmt.Errorf("the text was typed, but Enter failed: %w", err)
		}
	}
	msg := fmt.Sprintf("typed %d characters in %s by tapping the keys of the keyboard", len(runes), where)
	if pkg != "" {
		msg += " " + pkg
	}
	if a.Submit {
		msg += ", then pressed Enter"
	}
	return mcp.TextResult(msg), nil
}

// KeyFor finds the key of the keyboard window that types r: the one visible node whose description or text
// is r, or for a space or a new line the label that a keyboard gives them. It gives the key and the package
// of the keyboard. When there is none, or several that cannot be told apart, the error says so, and for a
// letter that the other case would type it says to tap shift first.
func KeyFor(t *mobile.Tree, hints mobile.WindowHints, r rune) (*mobile.Node, string, error) {
	pkg, keys := keyboardKeys(t, hints)
	if pkg == "" {
		if hints.IME == "" {
			return nil, "", errors.New("the keyboard could not be told from the app: the input method is not known")
		}
		return nil, "", fmt.Errorf("the keyboard %s is not shown: tap the field first", hints.IME)
	}
	found := matchKeys(keys, r)
	switch len(found) {
	case 1:
		return found[0], pkg, nil
	case 0:
		return nil, pkg, fmt.Errorf("no key for %s on %s: %s", quoteRune(r), pkg, missingHint(keys, r))
	default:
		return nil, pkg, fmt.Errorf("%d keys show %s on %s and cannot be told apart: tap the right one by its ref", len(found), quoteRune(r), pkg)
	}
}

func quoteRune(r rune) string {
	switch r {
	case ' ':
		return "' ' (space)"
	case '\n':
		return "'\\n' (new line)"
	}
	return fmt.Sprintf("'%c'", r)
}

// missingHint says what to do when no key shows r: the key may be on the other case of the letters, or
// on another page of the keyboard.
func missingHint(keys []*mobile.Node, r rune) string {
	if other := swapCase(r); other != r && len(matchKeys(keys, other)) > 0 {
		if unicode.IsUpper(r) {
			return "tap shift first"
		}
		return "shift is on: tap shift to go back to small letters first"
	}
	return "this page of the keyboard has no such key: switch to the page that has it (symbols or numbers) first"
}

func swapCase(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	return unicode.ToUpper(r)
}

// keyboardKeys lists the visible labelled nodes of the keyboard windows, and the package of the keyboard.
func keyboardKeys(t *mobile.Tree, hints mobile.WindowHints) (string, []*mobile.Node) {
	pkg := ""
	var keys []*mobile.Node
	for _, w := range mobile.Windows(t, hints) {
		if w.Kind != mobile.WindowKeyboard {
			continue
		}
		pkg = w.Package
		var walk func(n *mobile.Node)
		walk = func(n *mobile.Node) {
			if n.Visible && !n.Bounds.Empty() && len(labels(n)) > 0 {
				keys = append(keys, n)
			}
			for _, k := range n.Children {
				walk(k)
			}
		}
		walk(w.Root)
	}
	return pkg, keys
}

// labels are the texts that a key shows to a reader: its description and its text.
func labels(n *mobile.Node) []string {
	var out []string
	for _, l := range []string{n.Desc, n.Text, n.Attrs["text"], n.Attrs["content-desc"]} {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

var (
	spaceLabels = []string{"space", "space bar", "spacebar"}
	enterLabels = []string{"enter", "return"}
)

// matchKeys gives the keys that type r. Keys with the same box count once, and when some of the keys are
// clickable the others (labels that sit on a key) are left out.
func matchKeys(keys []*mobile.Node, r rune) []*mobile.Node {
	var hit []*mobile.Node
	seen := map[mobile.Rect]bool{}
	for _, n := range keys {
		if !types(n, r) || seen[n.Bounds] {
			continue
		}
		seen[n.Bounds] = true
		hit = append(hit, n)
	}
	var clickable []*mobile.Node
	for _, n := range hit {
		if mobilerec.Clickable(n) {
			clickable = append(clickable, n)
		}
	}
	if len(clickable) > 0 {
		return clickable
	}
	return hit
}

func types(n *mobile.Node, r rune) bool {
	for _, l := range labels(n) {
		if l == string(r) {
			return true
		}
		var names []string
		switch r {
		case ' ':
			names = spaceLabels
		case '\n':
			names = enterLabels
		}
		for _, name := range names {
			if strings.EqualFold(l, name) {
				return true
			}
		}
	}
	return false
}
