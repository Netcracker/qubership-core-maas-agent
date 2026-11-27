# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666 AS builder

ARG BUILDPLATFORM

WORKDIR /src/

COPY ./maas-agent-service/ ./maas-agent-service/
WORKDIR /src/maas-agent-service

RUN go mod download

# Build
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o app


FROM ghcr.io/netcracker/qubership-core-base:alpine-2.5.15@sha256:a3af3751d9187ce7f8ffd67a5aa8465fee78056c79f964bfa7727ea00da681da

COPY --from=builder --chown=10001:0 --chmod=755 /src/maas-agent-service/app /app/maas-agent
COPY --chown=10001:0 maas-agent-service/application.yaml /app/

CMD ["/app/maas-agent"]
