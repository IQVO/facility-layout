# syntax=docker/dockerfile:1.7

# --- build stage ---
FROM golang:1.26-alpine@sha256:c95332c2af86b6d89b91bd0500f4b9529ccbd090a0d1855c6d1ceaa142ae8615 AS build
WORKDIR /src

# Cache go.mod/go.sum download separately from source so editing source
# code doesn't bust the module-download layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# BuildKit cache mounts for the module and build caches speed up repeat
# builds in CI without baking the cache into the image layers.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/facility ./cmd/facility && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/facility-projector ./cmd/facility-projector && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/facility-reports ./cmd/facility-reports && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp ./cmd/mcp

# --- runtime stage ---
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
# apk upgrade picks up any CVE fixes published to the 3.24 branch since the
# base image was last rebuilt (e.g. openssl point releases); ca-certificates
# is still pinned explicitly for a reproducible, auditable base layer.
RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates=20260909-r0 && \
    addgroup -g 1000 -S app && adduser -u 1000 -S app -G app
WORKDIR /app
COPY --from=build --chown=app:app /out/facility ./facility
COPY --from=build --chown=app:app /out/facility-projector ./facility-projector
COPY --from=build --chown=app:app /out/facility-reports ./facility-reports
COPY --from=build --chown=app:app /out/mcp ./mcp
COPY --from=build --chown=app:app /src/migrations ./migrations
USER 1000
EXPOSE 8080
ENTRYPOINT ["./facility"]
