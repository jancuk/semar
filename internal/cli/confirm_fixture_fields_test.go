package cli

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

// leaf is one field of a fixture that holds a value, its sentinel, and a way to change it.
type leaf struct {
	path   string
	shown  string // the text that the approval screen must hold
	change func()
}

// fillFixture gives every field of v a distinct value and returns the leaves. A map gets one entry
// and a slice one element, so every field of an element type is reached.
func fillFixture(v reflect.Value, path string, n *int, leaves *[]leaf) {
	next := func() int { *n++; return *n }
	switch v.Kind() {
	case reflect.String:
		s := fmt.Sprintf("v%03dx", next())
		v.SetString(s)
		*leaves = append(*leaves, leaf{path, s, func() { v.SetString(v.String() + "-changed") }})
	case reflect.Int64:
		if v.Type() != reflect.TypeFor[time.Duration]() {
			panic("unexpected integer field " + path)
		}
		d := time.Duration(next())*time.Minute + 7*time.Second
		v.SetInt(int64(d))
		*leaves = append(*leaves, leaf{path, " " + d.String(), func() { v.SetInt(v.Int() + int64(time.Second)) }})
	case reflect.Interface:
		s := fmt.Sprintf("v%03dx", next())
		v.Set(reflect.ValueOf(s))
		*leaves = append(*leaves, leaf{path, s, func() { v.Set(reflect.ValueOf(s + "-changed")) }})
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillFixture(v.Elem(), path, n, leaves)
	case reflect.Struct:
		for i := range v.NumField() {
			fillFixture(v.Field(i), path+"."+v.Type().Field(i).Name, n, leaves)
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillFixture(v.Index(0), path+"[0]", n, leaves)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		v.Set(m)
		key := reflect.New(v.Type().Key()).Elem()
		var keyLeaves []leaf
		fillFixture(key, path+".key", n, &keyLeaves)
		elem := reflect.New(v.Type().Elem()).Elem()
		var elemLeaves []leaf
		fillFixture(elem, path+".value", n, &elemLeaves)
		m.SetMapIndex(key, elem)
		// A map value cannot be addressed, so a change sets the entry again.
		for _, l := range keyLeaves {
			*leaves = append(*leaves, leaf{l.path, l.shown, func() {
				m.SetMapIndex(key, reflect.Value{})
				l.change()
				m.SetMapIndex(key, elem)
			}})
		}
		for _, l := range elemLeaves {
			*leaves = append(*leaves, leaf{l.path, l.shown, func() {
				l.change()
				m.SetMapIndex(key, elem)
			}})
		}
	default:
		panic(fmt.Sprintf("unexpected kind %s at %s", v.Kind(), path))
	}
}

// Every field that the hash of a fixture covers must be on the screen that a person reads before
// approving it. The fields come from the struct, so a field added later is checked too.
func TestFixtureApprovalShowsEveryHashedField(t *testing.T) {
	var fx domain.Fixture
	var leaves []leaf
	n := 0
	fillFixture(reflect.ValueOf(&fx).Elem(), "Fixture", &n, &leaves)
	text := strings.Join(fixtureLines(fx, domain.FixtureHash(fx)), "\n")
	hashed := 0
	for _, l := range leaves {
		before := domain.FixtureHash(fx)
		l.change()
		after := domain.FixtureHash(fx)
		if before == after {
			continue // not covered by the hash, such as apiVersion and kind
		}
		hashed++
		if !strings.Contains(text, l.shown) {
			t.Errorf("%s is covered by the hash but not shown (want %q in)\n%s", l.path, l.shown, text)
		}
	}
	if hashed < 20 {
		t.Errorf("only %d hashed fields were found; the walk missed some", hashed)
	}
}
