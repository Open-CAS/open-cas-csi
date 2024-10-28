# CA_CERTS lines are replaced by make/deploy scripts
#FROM_CA_CERTS
FROM golang:1.21 as builder
ARG TARGETOS
ARG TARGETARCH
ARG http_proxy
ARG https_proxy
ARG GOPROXY

ENV KUBECTL_VERSION=v1.28.3

RUN apt-get update && apt-get install -y curl
#COPY_CA_CERTS
#INSTALL_CA_CERTS

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download && \
    curl -LO https://storage.googleapis.com/kubernetes-release/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl

# Copy the go source
COPY cmd/csi-plugin/main.go cmd/csi-plugin/main.go
COPY pkg/csi-driver/ pkg/csi-driver/
COPY pkg/opencas/ pkg/opencas/
COPY pkg/k8sutils/ pkg/k8sutils/
COPY pkg/utils/ pkg/utils/
COPY pkg/webhook/ pkg/webhook/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o csi-plugin ./cmd/csi-plugin

# build with: CGO_ENABLED=0 go build -C ./bin ../cmd/csi-plugin
FROM alpine

ARG http_proxy
ARG https_proxy

WORKDIR /
COPY --from=builder /workspace/csi-plugin .
COPY --from=builder /workspace/kubectl /bin/kubectl
COPY --from=builder /workspace/pkg/webhook/deploy/ ./
RUN apk add util-linux e2fsprogs xfsprogs openssl && \
    chmod +x /bin/kubectl && \
    chmod +x ./*.sh
USER root

ENTRYPOINT ["/csi-plugin"]
