.DEFAULT_GOAL := all

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


all: lint build

.PHONY: clean
clean:
	rm -rf ./build

.PHONY: lint
lint: $(addprefix lint.,$(ALL_GO_SUBMODULES:/go.mod=))

.PHONY: lint.%
lint.%:
	cd $* && golangci-lint run

.PHONY: build
build: l4proxy service-announcer

.PHONY: l4proxy
l4proxy: l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH)

.PHONY: service-announcer
service-announcer: service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH)

.PHONY: l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH)
l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH):
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ./build/l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH) ./cmd/l4proxy

.PHONY: service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH)
service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH):
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ./build/service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH) ./cmd/service-announcer

.PHONY: release
release:
	GOOS=linux GOARCH=amd64 make build
	GOOS=darwin GOARCH=amd64 make build
	GOOS=linux GOARCH=arm64 make build
	GOOS=linux GOARCH=arm make build

.PHONY: test
test: $(addprefix test.,$(ALL_GO_SUBMODULES:/go.mod=))

.PHONY: test.%
test.%:
	cd $* && go test -race ./...
