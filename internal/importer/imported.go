// Package importer reads a Postman collection or a HAR file into a list of Imported requests, and
// turns that list into scenarios and scripts. The two readers parse at the boundary, and the writer
// trusts the typed value.
package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// MaxFileBytes is the largest file that Read takes.
const MaxFileBytes = 64 << 20

// MaxRequests is the most requests that one import turns into scenarios.
const MaxRequests = 500

// Header is one request header of the file.
type Header struct{ Name, Value string }

// Imported is one request of the file, before it becomes a scenario. A Postman variable is already
// replaced by the value that the collection gives.
type Imported struct {
	Name    string
	Method  string
	URL     string
	Headers []Header
	Body    []byte // JSON, or empty for no body
	Status  int    // the status of the recorded response, or of the first saved example; 0 for none
}

// Left is a request that the import did not take, and why.
type Left struct{ Name, Reason string }

// Format is the kind of file.
type Format string

const (
	Postman Format = "Postman collection"
	HAR     Format = "HAR file"
)

// File is what a reader found.
type File struct {
	Format   Format
	Requests []Imported
	Left     []Left
	// WithTests counts the requests of a Postman collection that had a test script. Semar reads none.
	WithTests int
}

// ErrNotSupported is the one sentence for a file that is neither format.
var ErrNotSupported = errors.New("the file is neither a Postman collection (format 2.1) nor a HAR file")

// ErrTooLarge is the one sentence for a file over MaxFileBytes.
var ErrTooLarge = fmt.Errorf("the file is larger than %d MB, which is more than semar import reads", MaxFileBytes>>20)

// Read finds the format from the content and reads the file. drop says which header the reader leaves
// out before it looks at its value.
func Read(data []byte, drop func(header string) bool) (File, error) {
	if drop == nil {
		drop = func(string) bool { return false }
	}
	if len(data) > MaxFileBytes {
		return File{}, ErrTooLarge
	}
	var probe struct {
		Info *json.RawMessage `json:"info"`
		Item *json.RawMessage `json:"item"`
		Log  *struct {
			Entries *json.RawMessage `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &probe); err != nil {
		return File{}, ErrNotSupported
	}
	var f File
	var err error
	switch {
	case probe.Info != nil && probe.Item != nil:
		f, err = readPostman(data, drop)
	case probe.Log != nil && probe.Log.Entries != nil:
		f, err = readHAR(data, drop)
	default:
		return File{}, ErrNotSupported
	}
	if err != nil {
		return File{}, err
	}
	if len(f.Requests) > MaxRequests {
		return File{}, fmt.Errorf("the file holds %d requests, and semar import takes at most %d at once; split the file", len(f.Requests), MaxRequests)
	}
	return f, nil
}
