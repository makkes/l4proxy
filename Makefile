.DEFAULT_GOAL := all

all: lint build

.PHONY: lint
lint: 
	golangci-lint run

.PHONY: build
build:
	go build -o l4proxy
