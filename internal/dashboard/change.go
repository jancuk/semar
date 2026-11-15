package dashboard

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/userconfig"
)

const keychainScheme = "keychain:"

// statusError is a refusal with the HTTP status that says why.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }

func refuse(code int, format string, args ...any) error {
	return &statusError{code: code, msg: fmt.Sprintf(format, args...)}
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.code
	}
	return http.StatusInternalServerError
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if err := idcheck.Check("provider id", id); err != nil {
		fail(w, http.StatusBadRequest, "The provider ID in the address is not valid: use letters, digits, dot, underscore, or hyphen, starting with a letter or digit.")
		return "", false
	}
	return id, true
}

// change loads the file, lets edit change it, and saves it, all under the lock. A file that
// is not valid on disk is never overwritten: the person fixes it by hand first.
func (s *Server) change(w http.ResponseWriter, edit func(f *userconfig.File) error) bool {
	return s.changeThen(w, edit, nil)
}

// changeThen runs after, when it is not nil, once the file is saved and still under the lock,
// so that no other change sees the file and the keychain disagree.
func (s *Server) changeThen(w http.ResponseWriter, edit func(f *userconfig.File) error, after func() error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, _, err := userconfig.Load(s.d.Home)
	if err != nil {
		fail(w, http.StatusConflict, "The providers file has an error, so the dashboard changes nothing until it is fixed by hand: "+scrub(err.Error()))
		return false
	}
	if err := edit(&f); err != nil {
		fail(w, statusOf(err), scrub(err.Error()))
		return false
	}
	if err := f.Validate(); err != nil {
		fail(w, http.StatusBadRequest, scrub(err.Error()))
		return false
	}
	if err := userconfig.Save(s.d.Home, f); err != nil {
		fail(w, http.StatusInternalServerError, "The providers file could not be saved: "+scrub(err.Error()))
		return false
	}
	if after != nil {
		if err := after(); err != nil {
			fail(w, statusOf(err), scrub(err.Error()))
			return false
		}
	}
	return true
}

func find(f *userconfig.File, id string) int {
	return slices.IndexFunc(f.Providers, func(p userconfig.Provider) bool { return p.ID == id })
}

// putProvider creates or replaces one provider. With If-None-Match: * it only creates, so
// that the form that adds a provider cannot replace one by accident.
func (s *Server) putProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body definition
	if !decode(w, r, &body, false) {
		return
	}
	if body.ID != id {
		fail(w, http.StatusBadRequest, "The provider ID in the address and in the body must be the same.")
		return
	}
	onlyCreate := r.Header.Get("If-None-Match") == "*"
	p := userconfig.Provider{ID: body.ID, Kind: body.Kind, Model: strings.TrimSpace(body.Model), BaseURL: strings.TrimSpace(body.BaseURL), APIKey: strings.TrimSpace(body.APIKey)}
	if body.Setup != nil {
		p.Setup = &userconfig.Setup{MCP: body.Setup.MCP, Skills: body.Setup.Skills}
		for _, d := range body.Setup.Deny {
			if d = strings.TrimSpace(d); d != "" {
				p.Setup.Deny = append(p.Setup.Deny, d)
			}
		}
	}
	created := false
	if !s.change(w, func(f *userconfig.File) error {
		i := find(f, id)
		switch {
		case i >= 0 && onlyCreate:
			return refuse(http.StatusPreconditionFailed, "A provider with the ID %s exists already. Choose another ID.", id)
		case i >= 0:
			f.Providers[i] = p
		default:
			f.Providers = append(f.Providers, p)
			created = true
		}
		return nil
	}) {
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	reply(w, code, toDefinition(p))
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok || !decode(w, r, &struct{}{}, true) {
		return
	}
	var removed userconfig.Provider
	var others []userconfig.Provider
	deleteKey := func() error {
		name, isKeychain := strings.CutPrefix(removed.APIKey, keychainScheme)
		shared := slices.ContainsFunc(others, func(p userconfig.Provider) bool { return strings.EqualFold(p.APIKey, removed.APIKey) })
		if isKeychain && !shared {
			if err := s.d.Keys.Delete(name); err != nil && !errors.Is(err, keychain.ErrNotFound) {
				return refuse(http.StatusInternalServerError, "The provider was removed, but its key could not be deleted from the keychain: %v", err)
			}
		}
		return nil
	}
	if !s.changeThen(w, func(f *userconfig.File) error {
		i := find(f, id)
		switch {
		case i < 0:
			return refuse(http.StatusNotFound, "There is no provider with the ID %s.", id)
		case f.Default == id:
			return refuse(http.StatusConflict, "%s is the default provider. Make another provider the default first.", id)
		}
		removed = f.Providers[i]
		f.Providers = slices.Delete(f.Providers, i, i+1)
		others = f.Providers
		return nil
	}, deleteKey) {
		return
	}
	reply(w, http.StatusOK, struct {
		Removed string `json:"removed"`
	}{id})
}

func (s *Server) putDefault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if !s.change(w, func(f *userconfig.File) error {
		if find(f, body.ID) < 0 {
			return refuse(http.StatusNotFound, "There is no provider with that ID.")
		}
		f.Default = body.ID
		return nil
	}) {
		return
	}
	reply(w, http.StatusOK, struct {
		Default string `json:"default"`
	}{body.ID})
}

// putKey stores a pasted key in the keychain under the provider's ID. The value is never
// written to an answer, an error, or a log.
func (s *Server) putKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	value := strings.TrimSpace(body.Value)
	if value == "" {
		fail(w, http.StatusBadRequest, "The key is empty. Paste the key and try again.")
		return
	}
	if err := keychain.CheckName(id); err != nil {
		fail(w, http.StatusBadRequest, "This provider ID cannot name a keychain entry: "+err.Error())
		return
	}
	ref := keychainScheme + id
	if !s.change(w, func(f *userconfig.File) error {
		i := find(f, id)
		switch {
		case i < 0:
			return refuse(http.StatusNotFound, "There is no provider with the ID %s.", id)
		case !provider.IsAPI(f.Providers[i].Kind):
			return refuse(http.StatusBadRequest, "%s signs in by itself and takes no key.", id)
		}
		for j, p := range f.Providers {
			if j != i && strings.EqualFold(p.APIKey, ref) {
				return refuse(http.StatusConflict, "The provider %s uses the keychain entry %s as its key, and storing a key for %s would replace it. Change the apiKey of %s first.", p.ID, p.APIKey, id, p.ID)
			}
		}
		// The key is stored before the file names it, so the file never points at an
		// entry that a failed write left out.
		if err := s.d.Keys.Set(id, value); err != nil {
			return refuse(http.StatusInternalServerError, "The key could not be stored in the keychain: %v", err)
		}
		f.Providers[i].APIKey = ref
		return nil
	}) {
		return
	}
	reply(w, http.StatusOK, struct {
		APIKey string `json:"apiKey"`
	}{ref})
}
