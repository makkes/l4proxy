.DEFAULT_GOAL := all

GORELEASER_DEBUG ?= false
GORELEASER_PARALLELISM ?= $(shell nproc --ignore=1)

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ALL_GO_SUBMODULES := $(shell PATH='$(PATH)'; find -mindepth 2 -maxdepth 2 -name go.mod -printf '%P\n' | sort)

GIT_COMMIT := $(shell git rev-parse "HEAD^{commit}")
ifneq ($(shell git status --porcelain 2>/dev/null; echo $$?), 0)
    GIT_TREE_STATE := dirty
endif

GIT_TAG := $(shell git describe --tags --abbrev=7 "$(GIT_COMMIT)^{commit}" --exact-match 2>/dev/null)
ifeq (, $(GIT_TAG))
    GIT_VERSION := $(shell git describe --tags --abbrev=7 --always --dirty)
else
    GIT_VERSION := $(GIT_TAG)$(if $(GIT_TREE_STATE),-$(GIT_TREE_STATE))
endif


all: lint build-snapshot

.PHONY: lint
lint: $(addprefix lint.,$(ALL_GO_SUBMODULES:/go.mod=))

.PHONY: lint.%
lint.%:
	cd $* && golangci-lint run

.PHONY: build-snapshot
build-snapshot:
	goreleaser --debug=$(GORELEASER_DEBUG) \
		build \
		--snapshot \
		--clean \
		--parallelism=$(GORELEASER_PARALLELISM) \
		--single-target \
		--skip-post-hooks

.PHONY: release-snapshot
release-snapshot:
	goreleaser --debug=$(GORELEASER_DEBUG) \
		release \
		--snapshot \
		--clean \
		--parallelism=$(GORELEASER_PARALLELISM) \
		--skip-publish

.PHONY: release
release:
	goreleaser --debug=$(GORELEASER_DEBUG) \
		release \
		--clean \
		--parallelism=$(GORELEASER_PARALLELISM)

.PHONY: test
test: $(addprefix test.,$(ALL_GO_SUBMODULES:/go.mod=))

.PHONY: test.%
test.%:
	cd $* && go test -race ./...
