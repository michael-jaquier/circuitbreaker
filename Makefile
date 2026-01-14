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
lint: gofmt golint staticcheck check-examples

.PHONY: staticcheck
staticcheck: bin/staticcheck
	@$(GOBIN)/staticcheck ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: vet-examples
vet-examples:
	@echo "Vetting examples..."
	@cd examples/http_client && go vet .
	@cd examples/repository_pattern && go vet .
	@cd examples/grpc_service && go vet .
	@echo "Examples vet passed"

.PHONY: lint-examples
lint-examples:
	@echo "Linting examples..."
	@cd examples/http_client && go fmt . && golint .
	@cd examples/repository_pattern && go fmt . && golint .
	@cd examples/grpc_service && go fmt . && golint .
	@echo "Examples lint passed"

.PHONY: build-examples
build-examples:
	@echo "Building examples..."
	@cd examples/http_client && go build -o /dev/null .
	@cd examples/repository_pattern && go build -o /dev/null .
	@cd examples/grpc_service && go build -o /dev/null .
	@echo "Examples build passed"

.PHONY: check-examples
check-examples: vet-examples build-examples
	@echo "All example checks passed"

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
