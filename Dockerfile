# Pinned to the builder's platform so multi-arch images cross-compile with Go
# instead of running the whole toolchain under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.24 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /dispatcharr-rematch ./cmd/dispatcharr-rematch

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /dispatcharr-rematch /dispatcharr-rematch
USER 65532:65532
ENTRYPOINT ["/dispatcharr-rematch"]
