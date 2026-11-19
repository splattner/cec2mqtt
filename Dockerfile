# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.24

FROM docker.io/library/golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# libcec from Alpine is built with support for both the Pulse-Eight adapter and
# the Linux kernel CEC API (/dev/cecX), so it no longer needs to be compiled here.
RUN apk add --no-cache build-base pkgconf libcec-dev

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY *.go ./

ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... \
 && go build -trimpath -ldflags "-s -w -X main.BuildVersion=${VERSION}" -o /out/cec2mqtt .

FROM docker.io/library/alpine:${ALPINE_VERSION}

RUN apk add --no-cache libcec

COPY --from=builder /out/cec2mqtt /usr/bin/cec2mqtt

VOLUME /data/cec2mqtt

ENTRYPOINT ["/usr/bin/cec2mqtt"]
