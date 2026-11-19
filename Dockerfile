# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.24
ARG XX_VERSION=1.10.0

FROM --platform=$BUILDPLATFORM docker.io/tonistiigi/xx:${XX_VERSION} AS xx

# The builder always runs on the platform of the build machine and cross
# compiles for the target platform using xx. This is much faster than building
# under QEMU emulation, which took ~10 minutes per ARM platform.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

COPY --from=xx / /

# Native toolchain to cross compile the cgo parts with.
RUN apk add --no-cache clang lld pkgconf

ARG TARGETPLATFORM

# Target platform libraries to link against. libcec from Alpine is built with
# support for both the Pulse-Eight adapter and the Linux kernel CEC API (/dev/cecX).
RUN xx-apk add --no-cache gcc musl-dev libcec-dev

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY *.go ./

# go vet and the tests run in CI (and are platform independent), so they're not repeated here.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build,id=go-build-$TARGETPLATFORM \
    CGO_ENABLED=1 xx-go build -trimpath -ldflags "-s -w -X main.BuildVersion=${VERSION}" -o /out/cec2mqtt . \
 && xx-verify /out/cec2mqtt

FROM docker.io/library/alpine:${ALPINE_VERSION}

RUN apk add --no-cache libcec

COPY --from=builder /out/cec2mqtt /usr/bin/cec2mqtt

VOLUME /data/cec2mqtt

ENTRYPOINT ["/usr/bin/cec2mqtt"]
