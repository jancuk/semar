package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/pagelist"
)

// pageListJS lists the elements of the page that a person can act on or read, and keeps them in
// window.__semarRefs, in the order that it returns them, so that a reference of the list can be
// turned back into an element. It reads no value of a password field.
//
//go:embed pagelist.js
var pageListJS string

// Explorer reads the page of a recorder's tab as a list of references and acts on a reference as a
// person acts on what they see: with the mouse and the keyboard of the browser, so that the recorder
// hears the same events as it hears from a person.
type Explorer struct {
	rec  *Recorder
	mu   sync.Mutex
	list pagelist.List
}

// NewExplorer starts to explore the tab of a recorder.
func NewExplorer(rec *Recorder) *Explorer { return &Explorer{rec: rec} }

// List reads the page as it is now. The references of an earlier list stop to be valid.
func (x *Explorer) List(ctx context.Context) (pagelist.List, error) {
	var raw struct {
		URL   string          `json:"url"`
		Title string          `json:"title"`
		Items []pagelist.Item `json:"items"`
	}
	text, err := chromedp.Run(x.rec.tab, chromedp.Evaluate[string](pageListJS))
	if err != nil {
		return pagelist.List{}, fmt.Errorf("the page could not be read: %w", err)
	}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return pagelist.List{}, errors.New("the page list is not readable")
	}
	l := pagelist.Build(raw.URL, raw.Title, raw.Items)
	x.mu.Lock()
	x.list = l
	x.mu.Unlock()
	return l, nil
}
