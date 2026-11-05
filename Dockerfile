# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:b76020929b709ddb6202afc05410a0faefae20ae05e27ad548b3870092688a69 AS builder

ARG BUILDPLATFORM

WORKDIR /src/

COPY ./maas-agent-service/ ./maas-agent-service/
WORKDIR /src/maas-agent-service

RUN go mod download

# Build
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o app


FROM ghcr.io/netcracker/qubership-core-base:alpine-2.5.11@sha256:1b80a6ba225210f2308c68679aa40e5211d4712269200a55862ec2a9061a55de

COPY --from=builder --chown=10001:0 --chmod=755 /src/maas-agent-service/app /app/maas-agent
COPY --chown=10001:0 maas-agent-service/application.yaml /app/

CMD ["/app/maas-agent"]
