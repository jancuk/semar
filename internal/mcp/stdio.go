package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// stdioGrace is how long ServeStdio waits for calls in flight when the input ends or ctx is done.
// Tests shorten it.
var stdioGrace = 5 * time.Second

// frameWriter writes one frame per line. One mutex covers every writer, so frames never interleave.
type frameWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frameWriter) write(frame []byte) {
	line := make([]byte, 0, len(frame)+1)
	line = append(append(line, frame...), '\n')
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = f.w.Write(line)
}

func (f *frameWriter) notify(method string, params any) {
	data, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", method, params})
	if err == nil {
		f.write(data)
	}
}

type stdioLine struct {
	data    []byte
	tooLong bool
	err     error // io.EOF at the end of the input
}

// ServeStdio serves newline-delimited JSON-RPC: one message or batch per line on r, one frame per line
// on w. Stdout must carry nothing else, so log to stderr. A line over MaxMessage gets a -32700
// answer with a null id and is skipped. Each request runs in its own goroutine. When the input ends,
// calls in flight get a few seconds to finish, then they are cancelled; when ctx ends they are
// cancelled at once. Either way ServeStdio waits up to five seconds and returns. It returns nil for a
// clean end of input or ctx, and the error of r otherwise.
func ServeStdio(ctx context.Context, s *Server, r io.Reader, w io.Writer) error {
	grace := stdioGrace
	fw := &frameWriter{w: w}
	sess := NewSession("stdio")
	sess.SetNotifier(fw.notify)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := make(chan struct{})
	defer close(stop)
	lines := make(chan stdioLine)
	go readLines(r, lines, stop)

	var wg sync.WaitGroup
	wait := func(d time.Duration) bool {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
			return true
		case <-time.After(d):
			return false
		}
	}

	for {
		select {
		case <-ctx.Done():
			cancel()
			wait(grace)
			return nil
		case ln := <-lines:
			switch {
			case ln.err != nil:
				if !wait(grace) {
					cancel()
					wait(grace)
				}
				if ln.err == io.EOF {
					return nil
				}
				return ln.err
			case ln.tooLong:
				fw.write(Response{Error: &Error{Code: CodeParseError, Message: "The line is larger than 4 MiB."}}.Marshal())
			case len(bytes.TrimSpace(ln.data)) == 0:
			default:
				started := make(chan struct{})
				wg.Add(1)
				go func(data []byte) {
					defer wg.Done()
					if resp := s.handle(runCtx, sess, data, func() { close(started) }); resp != nil {
						fw.write(resp)
					}
				}(ln.data)
				<-started
			}
		}
	}
}

// readLines sends each line of r, then one final item with the error (io.EOF at the end).
func readLines(r io.Reader, out chan<- stdioLine, stop <-chan struct{}) {
	send := func(l stdioLine) bool {
		select {
		case out <- l:
			return true
		case <-stop:
			return false
		}
	}
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	over := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !over {
			buf = append(buf, chunk...)
			if len(buf) > MaxMessage+2 {
				over, buf = true, nil
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if len(buf) > 0 || over {
			line := bytes.TrimRight(buf, "\r\n")
			if over || len(line) > MaxMessage {
				if !send(stdioLine{tooLong: true}) {
					return
				}
			} else if !send(stdioLine{data: line}) {
				return
			}
			buf, over = nil, false
		}
		if err != nil {
			send(stdioLine{err: err})
			return
		}
	}
}
