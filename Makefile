# Directory to put `go install`ed binaries in.
export GOBIN ?= $(shell pwd)/bin

GO_FILES := $(shell \
	find . '(' -path '*/.*' -o -path './vendor' ')' -prune \
	-o -name '*.go' -print | cut -b3-)

.PHONY: build
build:
	go build ./...

.PHONY: cover
cover:
	go test -coverprofile=cover.out -coverpkg=./... -v ./...
	go tool cover -html=cover.out -o cover.html

.PHONY: gofmt
gofmt:
	$(eval FMT_LOG := $(shell mktemp -t gofmt.XXXXX))
	@gofmt -e -s -l $(GO_FILES) > $(FMT_LOG) || true
	@[ ! -s "$(FMT_LOG)" ] || (echo "gofmt failed:" | cat - $(FMT_LOG) && false)

bin/golint:
	go install golang.org/x/lint/golint@latest

bin/staticcheck:
	go install honnef.co/go/tools/cmd/staticcheck@latest

bin/gosec:
	go install github.com/securego/gosec/v2/cmd/gosec@latest

bin/govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: golint
golint: bin/golint
	@$(GOBIN)/golint -set_exit_status ./...

.PHONY: lint
lint: gofmt golint staticcheck

.PHONY: staticcheck
staticcheck: bin/staticcheck
	@$(GOBIN)/staticcheck ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: gosec
gosec: bin/gosec
	@$(GOBIN)/gosec -quiet ./...

.PHONY: govulncheck
govulncheck: bin/govulncheck
	@$(GOBIN)/govulncheck ./...

.PHONY: security
security: gosec govulncheck

.PHONY: test
test:
	go test -race ./...

.PHONY: release
release: vet lint test security build

.PHONY: clean
clean:
	rm -rf bin/
	rm -f cover.out cover.html

.PHONY: all
all: lint test build
