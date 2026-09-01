.PHONY: help dev-backend build db-migrate db-migrate-down db-seed \
        test test-backend lint clean mocks mocks-check tools-mockery \
        openapi protos build-multiarch build-multiarch-e2e build-multiarch-all

# Default target
help:
	@echo "FastGateway Development Commands"
	@echo ""
	@echo "Development Commands:"
	@echo "  make dev-backend     - Run backend locally (requires postgres)"
	@echo ""
	@echo "Database Commands:"
	@echo "  make db-migrate      - Run database migrations"
	@echo "  make db-migrate-down - Rollback last migration"
	@echo "  make db-seed         - Seed database with initial data"
	@echo ""
	@echo "Testing Commands:"
	@echo "  make test            - Run all tests"
	@echo "  make test-backend    - Run backend tests"
	@echo "  make mocks           - Regenerate internal/mocks from .mockery.yml"
	@echo "  make mocks-check     - Fail if the checked-in mocks are stale"
	@echo ""
	@echo "OpenAPI Commands:"
	@echo "  make openapi         - Bundle docs/openapi/ into cmd/server/openapi.yaml"
	@echo ""
	@echo "Protobuf Commands:"
	@echo "  make protos          - Regenerate Go gRPC stubs from e2e/testdata/protos"
	@echo ""
	@echo "Multi-arch Build Commands:"
	@echo "  make build-multiarch          - Build & push backend (amd64+arm64)"
	@echo "  make build-multiarch-e2e      - Build & push e2e services (amd64+arm64)"
	@echo "  make build-multiarch-all      - Build & push all services (amd64+arm64)"
	@echo "  REGISTRY=myrepo TAG=v1.0 make build-multiarch  - Custom registry/tag"
	@echo ""
	@echo "Other Commands:"
	@echo "  make build           - Compile the backend binary"
	@echo "  make lint            - Run linters"
	@echo "  make clean           - Clean build artifacts"

# Load environment variables
ifneq (,$(wildcard ./.env))
    include .env
    export
endif

build:
	go build ./...

# Development commands
dev-backend:
	go run cmd/server/main.go

# Database commands
db-migrate:
	go run cmd/migrate/main.go up

db-migrate-down:
	go run cmd/migrate/main.go down

db-seed:
	go run cmd/seed/main.go

# Testing commands
test: test-backend

test-backend:
	go test -v -cover ./...

# Mocks
#
# Every mock in internal/mocks is generated from the interface it implements;
# see .mockery.yml. Run this after changing any repository or service
# interface, or any of the Kubernetes roles in internal/services/k8s_roles.go.
#
# mockery lives in tools/go.mod, a SEPARATE module, and is built into ./bin
# rather than installed with `go get -tool` into the root module. Controller
# ruling R11: pulling the generator into the product's module made its
# dependency graph participate in the product's version resolution, which
# silently bumped golang.org/x/crypto and the root `go` directive. Building it
# out-of-module keeps the root go.mod untouched.
MOCKERY := $(CURDIR)/bin/mockery

$(MOCKERY): tools/go.mod tools/go.sum
	@mkdir -p $(CURDIR)/bin
	cd tools && GOWORK=off go build -o $(MOCKERY) github.com/vektra/mockery/v3

.PHONY: tools-mockery
tools-mockery: $(MOCKERY)

mocks: $(MOCKERY)
	$(MOCKERY)

# mocks-check is what CI runs: regenerate, then fail if anything moved.
# Hand-maintained mocks are how the SetClientHeaderRepository /
# SetProjectRepository interface drift went unnoticed; this makes that class
# of defect impossible.
#
# It uses `git status --porcelain`, NOT `git diff`. `git diff` compares the
# index against the working tree and is therefore BLIND TO UNTRACKED FILES:
# a .mockery.yml edit that makes the generator emit a NEW file which is never
# committed produced no diff at all, and CI went green on a mock that does not
# exist in the repository -- defeating the entire point of the check.
#
# That was not hypothetical. `make mocks` generates
# internal/mocks/mock_kubernetes.go and internal/mocks/roles/, and neither
# appeared in `git diff --name-only -- internal/mocks/`.
#
# --porcelain reports modified, deleted AND untracked paths, so all three
# failure modes are caught. It also reads the index rather than writing to it,
# which `git add -N` would not.
mocks-check: mocks
	@drift="$$(git status --porcelain -- internal/mocks/)"; \
	if [ -n "$$drift" ]; then \
		echo "Generated mocks are stale, uncommitted or newly generated:"; \
		echo "$$drift"; \
		echo "Run 'make mocks' and commit the result."; \
		exit 1; \
	fi

# Linting
lint:
	golangci-lint run

# OpenAPI
openapi:
	npx @redocly/cli@latest bundle docs/openapi/openapi.yaml -o cmd/server/openapi.yaml
	@echo "OpenAPI spec bundled from docs/openapi/ to cmd/server/openapi.yaml"

# Protobuf/gRPC stub generation for the e2e test suite.
#
# Source: e2e/testdata/protos/*.proto. Output: e2e/testdata/pb/<pkg>/, one
# directory per proto package, rooted at PROTO_GO_PKG below. The proto files
# declare `option go_package = "./<pkg>";` (a bare relative path, not a
# valid Go import path) so each target is mapped explicitly with a
# protoc -M flag instead of editing the .proto files -- they must keep
# working with grpcurl for anyone debugging by hand.
#
# podinfo.proto is intentionally NOT generated: it declares the exact same
# `package echo` as echo.proto (identical Message and EchoService), and
# compiling both -- whether in one protoc invocation or as two separate Go
# packages linked into the same binary -- fails. In one invocation protoc
# rejects it outright ("echo.Message" is already defined in file
# "echo.proto"); generated into two separate Go packages it builds fine but
# panics at runtime the moment both are imported together, because the
# protobuf-go runtime registers types by their full proto name
# (protoregistry: `proto: file "podinfo.proto" has a name conflict over
# echo.Message`). Since the two files are byte-for-byte equivalent on the
# wire, the `echo` package generated from echo.proto covers both.
PROTO_DIR := e2e/testdata/protos
PB_DIR := e2e/testdata/pb
PROTO_GO_PKG := github.com/fastgateway-dev/backend-v2/e2e/testdata/pb

PROTO_MAP := echo.proto:echo \
             podinfo_delay.proto:delay \
             podinfo_env.proto:env \
             podinfo_header.proto:header \
             podinfo_info.proto:info \
             podinfo_token.proto:token \
             podinfo_version.proto:version

protos:
	@for pair in $(PROTO_MAP); do \
		f=$${pair%%:*}; pkg=$${pair##*:}; \
		mkdir -p $(PB_DIR)/$$pkg; \
		protoc -I $(PROTO_DIR) \
			--go_out=$(PB_DIR)/$$pkg --go_opt=paths=source_relative \
			--go_opt=M$$f=$(PROTO_GO_PKG)/$$pkg \
			--go-grpc_out=$(PB_DIR)/$$pkg --go-grpc_opt=paths=source_relative \
			--go-grpc_opt=M$$f=$(PROTO_GO_PKG)/$$pkg \
			$(PROTO_DIR)/$$f; \
	done
	@echo "Generated protobuf/gRPC stubs under $(PB_DIR)/"

# Multi-arch build (requires: docker buildx create --name multiarch --use --bootstrap)
REGISTRY ?= fastgatewaydev
TAG ?= v0.1.0
PLATFORMS ?= linux/amd64,linux/arm64

build-multiarch:
	docker buildx build --platform $(PLATFORMS) -t $(REGISTRY)/backend:$(TAG) --push .

build-multiarch-e2e:
	docker buildx build --platform $(PLATFORMS) -t $(REGISTRY)/jwt-server:$(TAG) --push ./e2e/servers/jwt-server
	docker buildx build --platform $(PLATFORMS) -t $(REGISTRY)/external-auth:$(TAG) --push ./e2e/servers/external-auth
	docker buildx build --platform $(PLATFORMS) -t $(REGISTRY)/grpc-external-auth:$(TAG) --push ./e2e/servers/grpc-external-auth
	docker buildx build --platform $(PLATFORMS) -t $(REGISTRY)/ext-proc-server:$(TAG) --push ./e2e/servers/ext-proc-server
	docker buildx build --platform $(PLATFORMS) -t $(REGISTRY)/wasm-host:$(TAG) --push ./e2e/servers/wasm-host

build-multiarch-all: build-multiarch build-multiarch-e2e

# Clean
clean:
	rm -rf tmp
