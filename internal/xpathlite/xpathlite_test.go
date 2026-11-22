package xpathlite_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/xpathlite"
)

type node struct {
	class string
	attrs map[string]string
	kids  []*node
	label string
}

func (n *node) Name() string                 { return n.class }
func (n *node) Attr(k string) (string, bool) { v, ok := n.attrs[k]; return v, ok }
func (n *node) Children() []*node            { return n.kids }

func n(label, class string, attrs map[string]string, kids ...*node) *node {
	return &node{label: label, class: class, attrs: attrs, kids: kids}
}

// tree:
//
//	hierarchy
//	  FrameLayout
//	    LinearLayout
//	      TextView a  text=Total  desc=Order total
//	      TextView b  text=Pay    resource-id=com.shop:id/pay_label
//	      Button  c   text=Pay    enabled=true  resource-id=com.shop:id/pay
//	      Button  d   text=Cancel enabled=false
//	    ScrollView
//	      LinearLayout
//	        TextView e text=Row 1
//	        TextView f text=Row 2
func sample() *node {
	return n("root", "hierarchy", nil,
		n("frame", "android.widget.FrameLayout", nil,
			n("linear", "android.widget.LinearLayout", nil,
				n("a", "android.widget.TextView", map[string]string{"text": "Total", "content-desc": "Order total"}),
				n("b", "android.widget.TextView", map[string]string{"text": "Pay", "resource-id": "com.shop:id/pay_label"}),
				n("c", "android.widget.Button", map[string]string{"text": "Pay", "enabled": "true", "resource-id": "com.shop:id/pay"}),
				n("d", "android.widget.Button", map[string]string{"text": "Cancel", "enabled": "false"}),
			),
			n("scroll", "android.widget.ScrollView", nil,
				n("inner", "android.widget.LinearLayout", nil,
					n("e", "android.widget.TextView", map[string]string{"text": "Row 1"}),
					n("f", "android.widget.TextView", map[string]string{"text": "Row 2"}),
				),
			),
		),
	)
}

func labels(t *testing.T, expr string) string {
	t.Helper()
	e, err := xpathlite.Compile(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	var out []string
	for _, x := range xpathlite.Select(e, sample()) {
		out = append(out, x.label)
	}
	return strings.Join(out, ",")
}

func TestSelect(t *testing.T) {
	for expr, want := range map[string]string{
		"/hierarchy":                                                       "root",
		"/hierarchy/android.widget.FrameLayout":                            "frame",
		"/hierarchy/android.widget.FrameLayout/*":                          "linear,scroll",
		"/hierarchy/*/android.widget.LinearLayout/android.widget.Button":   "c,d",
		"/android.widget.FrameLayout":                                      "",
		"//android.widget.Button":                                          "c,d",
		"//*":                                                              "root,frame,linear,a,b,c,d,scroll,inner,e,f",
		"//android.widget.TextView":                                        "a,b,e,f",
		"//android.widget.Button[@text='Pay']":                             "c",
		"//*[@text='Pay']":                                                 "b,c",
		"//*[@text=\"Pay\"]":                                               "b,c",
		"//android.widget.Button[@text='Pay' and @enabled='true']":         "c",
		"//android.widget.Button[@text='Pay' and @enabled='false']":        "",
		"//*[contains(@text,'ow')]":                                        "e,f",
		"//*[contains(@content-desc, 'total')]":                            "a",
		"//*[contains(@content-desc,'Order')]":                             "a",
		"//*[starts-with(@resource-id,'com.shop:id/pay')]":                 "b,c",
		"//*[starts-with(@resource-id,'pay')]":                             "",
		"//android.widget.TextView[1]":                                     "a,e",
		"//android.widget.TextView[2]":                                     "b,f",
		"//android.widget.TextView[3]":                                     "",
		"//android.widget.TextView[last()]":                                "b,f",
		"//android.widget.LinearLayout[last()]/android.widget.TextView[2]": "b,f",
		"//android.widget.ScrollView//android.widget.TextView":             "e,f",
		"//android.widget.ScrollView/android.widget.TextView":              "",
		"//*[@text='Pay'][2]":                                              "c",
		"//*[@text='Pay'][1]":                                              "b",
		"//android.widget.Button[@enabled='true'][@text='Pay']":            "c",
		"//android.widget.Button[ @text = 'Pay' ]":                         "c",
		"/hierarchy//android.widget.Button[last()]":                        "d",
		"//android.widget.Button[@missing='x']":                            "",
		"//*[@text='']":                                                    "",
	} {
		if got := labels(t, expr); got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
}

func TestSelectOfNothing(t *testing.T) {
	e, _ := xpathlite.Compile("//*")
	if got := xpathlite.Select(e, (*node)(nil)); got != nil {
		t.Errorf("a nil root gave %v", got)
	}
	if got := xpathlite.Select((*xpathlite.Expr)(nil), sample()); got != nil {
		t.Errorf("a nil expression gave %v", got)
	}
}

func TestCompileRefusesWhatIsNotSupported(t *testing.T) {
	for expr, want := range map[string]string{
		"":                                     "empty",
		"   ":                                  "empty",
		"Button":                               "starts with /",
		"./Button":                             "starts with /",
		"//":                                   "class name",
		"//Button/..":                          "class name",
		"//Button/.":                           "class name",
		"//Button/text()":                      "functions",
		"//Button/following-sibling::Button":   "axes",
		"//Button[@a='1' or @b='2']":           "expected ]",
		"//Button[not(@a)]":                    "not()",
		"//Button[@a]":                         "only @attribute='value'",
		"//Button[@a!='1']":                    "only @attribute='value'",
		"//Button[@a=1]":                       "quoted",
		"//Button[@a='1]":                      "not closed",
		"//Button[0]":                          "from 1",
		"//Button[position()=1]":               "expected @attribute",
		"//Button[last()-1]":                   "expected ]",
		"//Button[1":                           "expected ]",
		"//Button[contains(@a)]":               "expected ,",
		"//Button[contains(a,'x')]":            "@attribute",
		"//Button[contains(@a,'x']":            "expected )",
		"//Button[text()='x']":                 "expected @attribute",
		"//Button | //Image":                   "expected / or //",
		"//Button[@a='1'andd @b='2']":          "expected ]",
		"//(Button)":                           "class name",
		"/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r": "more than",
		"//a[1][2][3][4][5][6][7]":             "more than",
		"//a[@a='1' and @a='1' and @a='1' and @a='1' and @a='1' and @a='1' and @a='1']": "more than",
		strings.Repeat("/a", 600): "longer than",
	} {
		_, err := xpathlite.Compile(expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one with %q", expr, err, want)
		}
		if xpathlite.Validate(expr) == nil {
			t.Errorf("%q: Validate accepts it", expr)
		}
	}
}

func TestExprStringIsTheSource(t *testing.T) {
	e, err := xpathlite.Compile("//Button[@a='b']")
	if err != nil || e.String() != "//Button[@a='b']" {
		t.Errorf("%v %v", e, err)
	}
}

func TestAHostileExpressionIsCutShort(t *testing.T) {
	// 30 levels of one-child nodes, each also holding 40 leaves: //*//*//*... would visit far more
	// than the limit if it were not counted.
	root := &node{class: "r"}
	cur := root
	for i := 0; i < 30; i++ {
		next := &node{class: "x"}
		cur.kids = append(cur.kids, next)
		for j := 0; j < 40; j++ {
			cur.kids = append(cur.kids, &node{class: "x"})
		}
		cur = next
	}
	e, err := xpathlite.Compile("//*//*//*//*//*//*//*[@a='1']")
	if err != nil {
		t.Fatal(err)
	}
	if got := xpathlite.Select(e, root); len(got) != 0 {
		t.Errorf("selected %d", len(got))
	}
}

func TestSelectKeepsDocumentOrderAndNoRepeats(t *testing.T) {
	// //LinearLayout//TextView reaches e and f once, though two layouts contain them or not.
	got := labels(t, "//android.widget.LinearLayout//android.widget.TextView")
	if got != "a,b,e,f" {
		t.Errorf("got %q", got)
	}
	_ = fmt.Sprint
}
