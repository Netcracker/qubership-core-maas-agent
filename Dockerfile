# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.24.2-alpine3.20 AS builder

ARG BUILDPLATFORM

WORKDIR /src/

COPY ./maas-agent-service/ ./maas-agent-service/
WORKDIR /src/maas-agent-service

RUN go mod download

# Build
RUN CGO_ENABLED=0 go build -a -o app


FROM ghcr.io/netcracker/qubership/core-base:1.0.0

COPY --from=builder --chown=10001:0 --chmod=755 /src/maas-agent-service/app /app/maas-agent
COPY --chown=10001:0 maas-agent-service/application.yaml /app/
COPY --chown=10001:0 maas-agent-service/policies.conf /app/

CMD ["/app/maas-agent"]
