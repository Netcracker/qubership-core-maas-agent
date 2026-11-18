# syntax=docker/dockerfile:1

ARG GO_BUILDER_IMAGE=golang:1.27.3@sha256:b76020929b709ddb6202afc05410a0faefae20ae05e27ad548b3870092688a69
ARG RUNTIME_IMAGE=ghcr.io/netcracker/qubership-core-base:alpine-2.5.9@sha256:ab860790e4ecce56f8df673495cd5745ce587fffa7d7eb210d6d098563ccb26d
FROM --platform=$BUILDPLATFORM ${GO_BUILDER_IMAGE} AS build

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ARG TARGETOS
ARG TARGETARCH
ARG SOURCE_DIR=.
ARG BOOTSTRAP_DIR=
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-p=2 GOMAXPROCS=2 GOMEMLIMIT=1GiB

WORKDIR /src/source/maas-agent-service
COPY ${SOURCE_DIR}/maas-agent-service/ .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,id=maas-agent-go-build,target=/root/.cache/go-build \
    mkdir -p /reports && \
    go test -mod=readonly -v -count=1 -timeout=15m -coverprofile=/reports/coverage.out ./... 2>&1 | tee /reports/unit-tests.log

RUN --mount=type=bind,target=/build-context \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,id=maas-agent-go-build,target=/root/.cache/go-build \
    set -eu; \
    mkdir -p /out; \
    cp application.yaml /out/; \
    if [[ -n "$BOOTSTRAP_DIR" ]]; then \
        cp -a "/build-context/$BOOTSTRAP_DIR" /src/private; \
        cd /src/private; \
        go test -mod=readonly -v -count=1 -timeout=15m ./... 2>&1 | tee /reports/bootstrap-tests.log; \
        cp application.yaml policies.conf /out/; \
    fi; \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -mod=readonly -o /out/maas-agent .; \
    go version -m /out/maas-agent > /reports/build-info.txt; \
    chmod 555 /out/maas-agent; \
    chmod 444 /out/*.yaml; \
    if [[ -f /out/policies.conf ]]; then chmod 444 /out/policies.conf; fi

FROM scratch AS artifact
COPY --from=build /reports/ /test-results/

FROM ${RUNTIME_IMAGE} AS run
COPY --chown=10001:0 --from=build /out/ /app/
WORKDIR /app
CMD ["/app/maas-agent"]
