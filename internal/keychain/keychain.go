// Package keychain stores API keys in the operating system's keychain and resolves
// the references that providers.yaml holds in place of a key.
package keychain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name under which every entry is stored.
const Service = "semar"

// ErrNotFound reports that no entry has the name.
var ErrNotFound = errors.New("no keychain entry with that name")

// Store keeps secrets by name. Its errors never hold a secret value.
type Store interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// CheckName accepts 1 to 64 letters, digits, dot, underscore, and hyphen. The error never
// quotes the name, because a person may have pasted a key where a name belongs.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return errors.New("a name uses 1 to 64 letters, digits, dot, underscore, or hyphen")
	}
	return nil
}

// OS returns the store of the operating system.
func OS() Store { return osStore{} }

type osStore struct{}

func (osStore) Get(name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", fmt.Errorf("read keychain entry: %w", err)
	}
	v, err := keyring.Get(Service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("keychain entry %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read keychain entry %s: %w", name, err)
	}
	return v, nil
}

func (osStore) Set(name, value string) error {
	if err := CheckName(name); err != nil {
		return fmt.Errorf("write keychain entry: %w", err)
	}
	if err := keyring.Set(Service, name, value); err != nil {
		return fmt.Errorf("write keychain entry %s: %w", name, err)
	}
	return nil
}

func (osStore) Delete(name string) error {
	if err := CheckName(name); err != nil {
		return fmt.Errorf("delete keychain entry: %w", err)
	}
	err := keyring.Delete(Service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keychain entry %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("delete keychain entry %s: %w", name, err)
	}
	return nil
}

// Memory returns a store that lives in the process, for tests.
func Memory() Store { return &memStore{m: map[string]string{}} }

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Get(name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", fmt.Errorf("read keychain entry: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	if !ok {
		return "", fmt.Errorf("keychain entry %s: %w", name, ErrNotFound)
	}
	return v, nil
}

func (s *memStore) Set(name, value string) error {
	if err := CheckName(name); err != nil {
		return fmt.Errorf("write keychain entry: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = value
	return nil
}

func (s *memStore) Delete(name string) error {
	if err := CheckName(name); err != nil {
		return fmt.Errorf("delete keychain entry: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; !ok {
		return fmt.Errorf("keychain entry %s: %w", name, ErrNotFound)
	}
	delete(s.m, name)
	return nil
}

// CheckRef accepts env:NAME and keychain:NAME. Its error never quotes the reference, because
// a person may have pasted a key into the field.
func CheckRef(ref string) error {
	_, _, err := parseRef(ref)
	return err
}

func parseRef(ref string) (scheme, name string, err error) {
	scheme, name, ok := strings.Cut(ref, ":")
	if !ok || (scheme != "env" && scheme != "keychain") {
		return "", "", errors.New("a key reference must be env:NAME or keychain:NAME, and the key itself must not be written here")
	}
	if err := CheckName(name); err != nil {
		return "", "", fmt.Errorf("the name in a %s: reference is not valid: %w", scheme, err)
	}
	return scheme, name, nil
}

// Resolver returns a function that turns env:NAME and keychain:NAME into the secret value.
// Its errors name the reference and never hold a value.
func Resolver(lookupEnv func(string) (string, bool), store Store) func(ref string) (string, error) {
	return func(ref string) (string, error) {
		scheme, name, err := parseRef(ref)
		if err != nil {
			return "", err
		}
		switch scheme {
		case "env":
			v, ok := lookupEnv(name)
			if !ok || v == "" {
				return "", fmt.Errorf("the environment variable of %s is not set or is empty", ref)
			}
			return v, nil
		default:
			if store == nil {
				return "", fmt.Errorf("no keychain is available for %s", ref)
			}
			v, err := store.Get(name)
			if errors.Is(err, ErrNotFound) {
				return "", fmt.Errorf("the keychain has no entry for %s; store the key again", ref)
			}
			if err != nil {
				return "", fmt.Errorf("the keychain could not be read for %s: %w", ref, err)
			}
			if v == "" {
				return "", fmt.Errorf("the keychain entry of %s is empty", ref)
			}
			return v, nil
		}
	}
}
