.PHONY: check build vet test fmt run tidy lint test-conformance test-e2e dist clean

# The one command that verifies the codebase. Keep it green.
check: build vet test

build:
	go build ./...

# --- release builds -----------------------------------------------------------

BINARY  := apm-registry
PKG     := github.com/xgentic/agent-package-manager-registry/internal/cli

# Overridable so the release workflow can stamp the tag it is building.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# -s -w drop the symbol table and DWARF (~30% off a 16MB binary); -trimpath
# keeps build-machine paths out of the shipped artefact.
LDFLAGS := -s -w \
	-X $(PKG).version=$(VERSION) \
	-X $(PKG).commit=$(COMMIT) \
	-X $(PKG).buildDate=$(DATE)

# The asset names here are a contract with install.sh and install.ps1:
# $(BINARY)-<os>-<arch>[.exe]. Change one and you must change all three.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

# CGO_ENABLED=0 is what makes these cross-compile and stay static; the SQLite
# driver is pure Go for exactly this reason (ADR-0002).
dist: clean
	@mkdir -p dist
	@echo "building $(VERSION) ($(COMMIT))"
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=''; \
		[ "$$os" = windows ] && ext='.exe'; \
		out="dist/$(BINARY)-$$os-$$arch$$ext"; \
		echo "  $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags '$(LDFLAGS)' -o "$$out" . || exit 1; \
	done
	@# Globbed by prefix so SHA256SUMS cannot list itself.
	@cd dist && { \
		if command -v sha256sum >/dev/null 2>&1; then sha256sum $(BINARY)-*; \
		else shasum -a 256 $(BINARY)-*; fi; \
	} > SHA256SUMS
	@echo "checksums:"
	@sed 's/^/  /' dist/SHA256SUMS

clean:
	rm -rf dist

vet:
	go vet ./...

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	go fmt ./...

tidy:
	go mod tidy

run:
	go run .

# MS-API §9 conformance suite. With no BASE_URL it spins up an in-process
# server; with one it runs the same assertions against a deployment (TR-22).
#   make test-conformance
#   make test-conformance BASE_URL=https://registry.example.com
test-conformance:
	BASE_URL=$(BASE_URL) go test -v -count=1 ./internal/conformance/...

# Round-trip against the real `apm` CLI (TR-26). Needs `apm` on PATH with the
# `registries` experimental feature available. Deliberately outside `check`.
test-e2e:
	./scripts/e2e.sh
