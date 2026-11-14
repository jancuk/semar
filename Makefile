VERSION ?= dev

.PHONY: build test check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/jancuk/semar/internal/cli.Version=$(VERSION)" -o bin/semar ./cmd/semar

test:
	go test -race ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	$(MAKE) test
