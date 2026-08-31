# Builds the gRPC-safe proxy-wasm filter and serves it over plain HTTP inside
# the cluster, so the e2e suite does not depend on fetching a third-party .wasm
# from the public internet.
#
# The module's sha256 is COMPUTED here and written next to it, never pinned in
# source. An earlier version pinned a hash produced on a developer machine and
# verified it here; that broke immediately, because the wasm bytes differ
# between Go patch releases (go1.25.2 locally vs go1.25.14 in the golang:1.25
# image). CI reads the computed hash out of this image and passes it to the
# tests, so no toolchain-reproducibility assumption is made anywhere.
FROM golang:1.25 AS build
WORKDIR /src
COPY filter/go.mod filter/go.sum ./
RUN go mod download
COPY filter/ ./
RUN GOOS=wasip1 GOARCH=wasm go build -trimpath -buildmode=c-shared \
        -ldflags="-s -w -buildid=" -o /grpcsafe.wasm .

# Sanity-check the artefact is a real wasm module (magic bytes \0asm) before
# shipping it, so a broken build fails here rather than as an opaque Envoy
# fetch error, then record its digest for the test run to consume.
RUN head -c4 /grpcsafe.wasm | od -An -tx1 | tr -d ' \n' | grep -qx '0061736d' \
      || { echo "grpcsafe.wasm is not a valid wasm module"; exit 1; }; \
    sha256sum /grpcsafe.wasm | cut -d' ' -f1 > /grpcsafe.wasm.sha256; \
    echo "grpcsafe.wasm sha256: $(cat /grpcsafe.wasm.sha256)"

FROM nginx:1.25-alpine
COPY --from=build /grpcsafe.wasm /usr/share/nginx/html/grpcsafe.wasm
COPY --from=build /grpcsafe.wasm.sha256 /usr/share/nginx/html/grpcsafe.wasm.sha256
