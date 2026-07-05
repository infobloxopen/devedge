MODULE   := github.com/infobloxopen/devedge
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -X $(MODULE)/internal/version.Version=$(VERSION) \
            -X $(MODULE)/internal/version.Commit=$(COMMIT)

GOBIN    ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN    := $(shell go env GOPATH)/bin
endif
PREFIX   ?= $(GOBIN)
DESTDIR  ?=
BINDIR   := $(DESTDIR)$(PREFIX)

BINS     := de devedged devedge-dns-webhook

.PHONY: all build test lint lint-api-paths clean install help docs-cli docs-cli-check
.DEFAULT_GOAL := help

##@ Development

all: test build ## Run tests then build

# devedge builds `de` itself, so its own Makefile does NOT delegate to `de`
# (that would be a bootstrap/CI chicken-and-egg). The `de sync` shim model is for
# consumer service projects. We still apply `-trimpath` uniformly for a
# reproducible build, matching `de build`, `de image`, and .goreleaser.yaml.
build: ## Compile all binaries into bin/
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/de ./cmd/de
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devedged ./cmd/devedged
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devedge-dns-webhook ./cmd/devedge-dns-webhook

test: ## Run the test suite
	go test ./...

lint: ## Run go vet
	go vet ./...

lint-api-paths: ## Reject the version-after-resource URL anti-pattern in protos
	bash hack/lint-api-paths.sh

docs-cli: ## Regenerate the CLI reference pages under docs/ from de --help
	bash hack/gen-cli-docs.sh

docs-cli-check: docs-cli ## Fail if the generated CLI reference pages are stale vs the de command set
	@if ! git diff --quiet -- docs/content/docs/reference/cli; then \
		echo "error: CLI reference pages are stale vs the 'de' command set."; \
		echo "run 'make docs-cli' and commit docs/content/docs/reference/cli."; \
		git --no-pager diff --stat -- docs/content/docs/reference/cli; \
		exit 1; \
	fi
	@echo "CLI reference pages are up to date."

##@ Installation

install: build ## Install binaries to $$(PREFIX)
	@mkdir -p $(BINDIR)
	@for bin in $(BINS); do \
		echo "  INSTALL  $(BINDIR)/$$bin"; \
		install -m 755 bin/$$bin $(BINDIR)/$$bin; \
	done
	@if [ -z "$(DESTDIR)" ]; then \
		case ":$(PATH):" in \
			*:$(BINDIR):*) ;; \
			*) echo "warning: $(BINDIR) is not in PATH" ;; \
		esac \
	fi

clean: ## Remove build artifacts and Go build cache
	rm -rf bin/
	go clean -cache

##@ Help

help: ## Show this help
	@ci="$$CI$$GITHUB_ACTIONS$$JENKINS_HOME$$JENKINS_URL$$GITLAB_CI$$TF_BUILD$$CIRCLECI$$TRAVIS$$BUILDKITE$$TEAMCITY_VERSION"; \
	if [ -t 1 ] && [ -z "$$ci" ]; then \
		BOLD="\033[1m"; CYAN="\033[36m"; RST="\033[0m"; \
	else \
		BOLD=""; CYAN=""; RST=""; \
	fi; \
	awk -v bold="$$BOLD" -v cyan="$$CYAN" -v rst="$$RST" \
	  'BEGIN { FS = ":.*##" } \
	   /^##@/ { printf "\n%s%s%s\n", bold, substr($$0, 5), rst; next } \
	   /^[a-zA-Z0-9_-]+:.*?##/ { printf "  %s%-10s%s %s\n", cyan, $$1, rst, $$2 }' \
	  $(MAKEFILE_LIST)
