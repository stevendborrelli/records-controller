FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/records-controller ./cmd/records-controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/records-controller /records-controller
USER 65532:65532
ENTRYPOINT ["/records-controller"]
