# Builds the gRPC-safe proxy-wasm filter and serves it over plain HTTP inside
# the cluster, so the e2e suite does not depend on fetching a third-party .wasm
# from the public internet.
#
# The build is byte-reproducible (-trimpath, -s -w, empty -buildid), which is
# what lets the tests pin a sha256. If the Go toolchain changes and the hash
# moves, this image FAILS TO BUILD with a clear message rather than letting
# Envoy reject the module later with an opaque checksum error.
FROM golang:1.25 AS build
WORKDIR /src
COPY filter/go.mod filter/go.sum ./
RUN go mod download
COPY filter/ ./
RUN GOOS=wasip1 GOARCH=wasm go build -trimpath -buildmode=c-shared \
        -ldflags="-s -w -buildid=" -o /grpcsafe.wasm .

ARG EXPECTED_SHA256=37d25f3ac04170dd3062e56a773664d330f9dc05743203c193f0a1518867bcf3
RUN actual=$(sha256sum /grpcsafe.wasm | cut -d' ' -f1); \
    if [ "$actual" != "$EXPECTED_SHA256" ]; then \
      echo "grpcsafe.wasm hash drift:"; \
      echo "  expected $EXPECTED_SHA256"; \
      echo "  actual   $actual"; \
      echo "The Go toolchain or filter source changed. Update the pinned sha256 in"; \
      echo "e2e/harness/wasmfilter.go and in this Dockerfile's EXPECTED_SHA256."; \
      exit 1; \
    fi; \
    echo "$actual" > /grpcsafe.wasm.sha256

FROM nginx:1.25-alpine
COPY --from=build /grpcsafe.wasm /usr/share/nginx/html/grpcsafe.wasm
COPY --from=build /grpcsafe.wasm.sha256 /usr/share/nginx/html/grpcsafe.wasm.sha256
