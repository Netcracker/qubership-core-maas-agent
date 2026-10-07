# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27.1@sha256:e0174e51e81218523251d85d248a90d24c3d5e81543b4f07a5d66229397db190 AS builder

ARG BUILDPLATFORM

WORKDIR /src/

COPY ./maas-agent-service/ ./maas-agent-service/
WORKDIR /src/maas-agent-service

RUN go mod download

# Build
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o app


FROM ghcr.io/netcracker/qubership-core-base:alpine-2.5.6@sha256:e85e971e33cdb218bf1c23ccbad0d0ddaee7d3abfc442247d81081e3d0467467

COPY --from=builder --chown=10001:0 --chmod=755 /src/maas-agent-service/app /app/maas-agent
COPY --chown=10001:0 maas-agent-service/application.yaml /app/

CMD ["/app/maas-agent"]
