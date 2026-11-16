package knowledge

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/idcheck"
)

// MaxTicketBytes bounds the text of one ticket file that the planner is given.
const MaxTicketBytes = 64 << 10

// Ticket is the file of one ticket of a change, as the copy of the source holds it.
type Ticket struct {
	Key  string
	Path string // the slash path of the file in the copy: <tickets folder>/<key>.md
	Text string // at most MaxTicketBytes
	Cut  bool   // the file is longer than MaxTicketBytes, and Text is its start
}

// CutTicket keeps the first MaxTicketBytes of a text, without half a character, and says whether
// it cut anything.
func CutTicket(text string) (string, bool) {
	if len(text) <= MaxTicketBytes {
		return text, false
	}
	end := MaxTicketBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}

// ReadTickets reads <path>/<key>.md of the first usable tickets source that has it, for each key,
// from the copy of the source at sourceDir. A key that passes no ID check, a name that is not in
// its folder with exactly that spelling, and anything that is not a regular file are left out: a
// link is never followed. The result is in key order. At most MaxTicketBytes of a file are read.
func ReadTickets(sourceDir string, sources []Source, keys []string) []Ticket {
	var folders []string
	for _, s := range sources {
		if s.Usable() && s.Kind == config.KnowledgeTickets && s.Folder {
			folders = append(folders, s.Path)
		}
	}
	if len(folders) == 0 || len(keys) == 0 {
		return nil
	}
	root, err := os.OpenRoot(sourceDir)
	if err != nil {
		return nil
	}
	defer root.Close()
	var out []Ticket
	for _, key := range slices.Sorted(slices.Values(slices.Compact(slices.Sorted(slices.Values(keys))))) {
		if idcheck.Check("ticket", key) != nil {
			continue
		}
		for _, folder := range folders {
			rel := path.Join(folder, key+".md")
			if !exactNames(root, rel) {
				continue
			}
			native := filepath.FromSlash(rel)
			if info, err := root.Lstat(native); err != nil || !info.Mode().IsRegular() {
				continue
			}
			data, err := readAtMost(root, native, MaxTicketBytes+1)
			if err != nil {
				continue
			}
			text, cut := CutTicket(strings.ToValidUTF8(string(data), "�"))
			out = append(out, Ticket{Key: key, Path: rel, Text: text, Cut: cut})
			break
		}
	}
	return out
}
