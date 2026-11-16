package knowledge_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/knowledge"
)

var ticketsSource = config.Knowledge{ID: "tickets", Kind: config.KnowledgeTickets, Path: "docs/tickets"}

func TestResolveTicketsIsAFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/tickets/SHOP-482.md", "# Move a card\n")
	write(t, dir, "docs/one.md", "x\n")
	got, err := knowledge.Resolve(dir, []config.Knowledge{
		ticketsSource,
		{ID: "file", Kind: config.KnowledgeTickets, Path: "docs/one.md"},
		{ID: "gone", Kind: config.KnowledgeTickets, Path: "docs/none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].Usable() || !got[0].Folder || got[0].Files != 1 {
		t.Errorf("tickets = %+v", got[0])
	}
	if got[1].Usable() || got[1].Problem != "docs/one.md is a file, and a tickets source is a folder" {
		t.Errorf("file problem = %q", got[1].Problem)
	}
	if got[2].Usable() || got[2].Problem != "docs/none is not in the copy of the source" {
		t.Errorf("gone problem = %q", got[2].Problem)
	}
}

func TestConventionalFindsTheTicketsFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/tickets/SHOP-1.md", "x")
	want := []config.Knowledge{docs, ticketsSource}
	if got := knowledge.Conventional(dir); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// A file named tickets is not a folder of tickets.
	other := t.TempDir()
	write(t, other, "docs/tickets", "x")
	for _, k := range knowledge.Conventional(other) {
		if k.Kind == config.KnowledgeTickets {
			t.Errorf("a file counted as the tickets folder: %+v", k)
		}
	}
	// The folder is found below a root too, and the path starts at the root.
	root := t.TempDir()
	write(t, root, "svc/docs/tickets/SHOP-1.md", "x")
	got := knowledge.ConventionalBelow(root, "svc")
	if len(got) != 2 || got[1].Path != "svc/docs/tickets" || got[1].Kind != config.KnowledgeTickets {
		t.Errorf("below = %+v", got)
	}
}

func TestReadTickets(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docs/tickets/SHOP-482.md", "# Move a card\n\nA move to another board is refused with 422.\n")
	write(t, dir, "docs/tickets/SHOP-9.md", "other\n")
	big := strings.Repeat("a line of a ticket\n", 5000)
	write(t, dir, "docs/tickets/SHOP-7.md", big)
	if err := os.Symlink("SHOP-9.md", filepath.Join(dir, "docs/tickets/SHOP-8.md")); err != nil {
		t.Skip("no links here")
	}
	srcs, err := knowledge.Resolve(dir, []config.Knowledge{ticketsSource})
	if err != nil {
		t.Fatal(err)
	}
	got := knowledge.ReadTickets(dir, srcs, []string{"SHOP-9", "SHOP-482", "SHOP-7", "SHOP-8", "SHOP-1", "../x"})
	var keys []string
	for _, tk := range got {
		keys = append(keys, tk.Key)
	}
	if want := []string{"SHOP-482", "SHOP-7", "SHOP-9"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v (no file, a link, and a bad key are left out)", keys, want)
	}
	if got[0].Path != "docs/tickets/SHOP-482.md" || got[0].Cut || !strings.Contains(got[0].Text, "refused with 422") {
		t.Errorf("first = %+v", got[0])
	}
	if !got[1].Cut || len(got[1].Text) > knowledge.MaxTicketBytes || len(got[1].Text) < knowledge.MaxTicketBytes-4 {
		t.Errorf("big ticket: cut %v, %d bytes", got[1].Cut, len(got[1].Text))
	}
}

func TestCutTicketKeepsUTF8(t *testing.T) {
	text := strings.Repeat("é", knowledge.MaxTicketBytes) // two bytes each
	cut, was := knowledge.CutTicket(text)
	if !was || len(cut) > knowledge.MaxTicketBytes || !strings.HasSuffix(cut, "é") {
		t.Errorf("cut %v, %d bytes", was, len(cut))
	}
	if same, was := knowledge.CutTicket("short"); was || same != "short" {
		t.Errorf("a short text changed: %q %v", same, was)
	}
}
