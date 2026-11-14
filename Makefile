VERSION ?= dev

# Tools started with "go run pkg@version" pick their own toolchain, which can be older than
# the standard library they must read. Pin every go command to the toolchain line of go.mod.
export GOTOOLCHAIN := $(shell awk '/^toolchain /{print $$2}' go.mod)

.PHONY: build test check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/jancuk/semar/internal/cli.Version=$(VERSION)" -o bin/semar ./cmd/semar

test:
	go test -race ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
	$(MAKE) test
