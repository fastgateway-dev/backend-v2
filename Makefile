.PHONY: help dev-backend build db-migrate db-migrate-down db-seed \
        test test-backend lint clean \
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
