# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.26@sha256:11fd8f7f63db3b6fb198797042ba4c40a4a34dc83325d3328ca3bc4bb7726786 AS builder

ARG BUILDPLATFORM

WORKDIR /src/

COPY ./maas-agent-service/ ./maas-agent-service/
WORKDIR /src/maas-agent-service

RUN go mod download

# Build
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o app



FROM ghcr.io/netcracker/qubership-core-base:2.3.0@sha256:3ef9a4b348dcf26d1e9f63d375209c9b4b2359e0080fbcab1a566f6f6291b789

COPY --from=builder --chown=10001:0 --chmod=755 /src/maas-agent-service/app /app/maas-agent
COPY --chown=10001:0 maas-agent-service/application.yaml /app/

CMD ["/app/maas-agent"]
