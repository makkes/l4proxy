.DEFAULT_GOAL := all

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

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
lint:
	golangci-lint run

.PHONY: build
build: l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH) service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH)

.PHONY: l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH)
l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH):
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ./build/l4proxy-$(GIT_VERSION)-$(GOOS)-$(GOARCH)

.PHONY: service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH)
service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH):
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ./build/service-announcer-$(GIT_VERSION)-$(GOOS)-$(GOARCH) ./service-announcer

.PHONY: release
release:
	GOOS=linux GOARCH=amd64 make build
	GOOS=darwin GOARCH=amd64 make build
	GOOS=linux GOARCH=arm64 make build
	GOOS=linux GOARCH=arm make build

.PHONY: test
test:
	go test -race ./...
