SHELL := /bin/sh

VERSION ?= v$(shell cat VERSION 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.DEFAULT_GOAL := help
.PHONY: help fmt vet test check build build-pi build-piccolo clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt: ## Format all Go source files
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet: ## Run Go static analysis
	go vet ./...

test: ## Run the test suite with the race detector
	go test -race -coverprofile=coverage.out ./...

check: vet test ## Run all local quality checks

build: build-pi build-piccolo ## Build both binaries

build-pi: ## Build the per-node Pi agent
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pi ./cmd/pi

build-piccolo: ## Build the central Piccolo API
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/piccolo ./cmd/piccolo

clean: ## Remove generated build and coverage artifacts
	rm -rf bin coverage.out
