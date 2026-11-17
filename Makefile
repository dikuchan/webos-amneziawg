GO_BIN ?= go
BUN ?= bun
TOOLS := $(CURDIR)/.build/tools
GO_PACKAGES := ./app ./tests/...
export PATH := $(TOOLS):$(PATH)
export GO_BIN BUN
.NOTPARALLEL:

.PHONY: setup tools fmt check format-check lint test test-web test-integration ui build package source release

setup: tools
	"$(BUN)" install --frozen-lockfile --ignore-scripts
	"$(BUN)" run build:ui

tools:
	GOBIN="$(TOOLS)" "$(GO_BIN)" install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
	GOBIN="$(TOOLS)" "$(GO_BIN)" install mvdan.cc/sh/v3/cmd/shfmt@v3.12.0
	sh scripts/setup-shellcheck.sh

fmt: ui
	golangci-lint fmt $(GO_PACKAGES)
	shfmt -w -i 2 packaging/*.sh scripts/*.sh tests/*.sh
	"$(BUN)" run format

format-check: ui
	@mkdir -p .build
	golangci-lint fmt --diff $(GO_PACKAGES) > .build/go-format.diff
	@if [ -s .build/go-format.diff ]; then cat .build/go-format.diff; exit 1; fi
	shfmt -d -i 2 packaging/*.sh scripts/*.sh tests/*.sh
	"$(BUN)" run format:check

ui:
	"$(BUN)" run build:ui

lint: ui
	golangci-lint run --build-tags browser $(GO_PACKAGES)
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 golangci-lint run --build-tags browser $(GO_PACKAGES)
	"$(BUN)" run typecheck
	"$(BUN)" run lint
	shellcheck packaging/*.sh scripts/*.sh tests/*.sh

test: ui
	"$(GO_BIN)" test -race $(GO_PACKAGES)
	"$(BUN)" run test

check: format-check lint test

test-web: ui
	"$(BUN)" run test:web

test-integration: build
	AWG_TEST_IMAGE="$${AWG_TEST_IMAGE:-$${AWG_BUILDER_IMAGE:-webos-amneziawg-builder}}" sh tests/split-network.sh

build:
	sh scripts/build-go.sh
	sh scripts/build-awg.sh

package: build
	"$(BUN)" scripts/package.ts

source:
	sh scripts/source.sh

release: check test-web package source
