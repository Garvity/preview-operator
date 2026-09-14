# syntax=docker/dockerfile:1
FROM golang:1.27.1-alpine AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY cmd/ cmd/
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /manager ./cmd/manager

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
