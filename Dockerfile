# syntax=docker/dockerfile:1.7

FROM node:20-alpine AS web
WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN rm -rf internal/assets/dist && mkdir -p internal/assets/dist
COPY --from=web /src/web/dist/ ./internal/assets/dist/

ARG TARGETOS=linux
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w" -o /out/muse-cc .
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/muse-cc /muse-cc
COPY --from=build --chown=65532:65532 /out/data /data

EXPOSE 8788
VOLUME ["/data"]

ENTRYPOINT ["/muse-cc"]
CMD ["-data", "/data"]
