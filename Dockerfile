# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS web
WORKDIR /src/web/app
COPY web/app/package.json web/app/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/app ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS go-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG NYARELAY_VERSION=0.1.3-dev
ARG NYARELAY_COMMIT=
ARG NYARELAY_BUILD_DATE=
ARG NYARELAY_UPDATE_PUBLIC_KEY=
WORKDIR /src
RUN mkdir -p /out/nodes
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN --mount=type=secret,id=nyarelay_update_public_key,required=false \
    if [ -s /run/secrets/nyarelay_update_public_key ]; then \
      UPDATE_PUBLIC_KEY="$(tr -d '[:space:]' < /run/secrets/nyarelay_update_public_key)"; \
    else \
      UPDATE_PUBLIC_KEY="$(printf '%s' "$NYARELAY_UPDATE_PUBLIC_KEY" | tr -d '[:space:]')"; \
    fi \
    && LDFLAGS="-X nyarelay/internal/shared/version.Version=${NYARELAY_VERSION} -X nyarelay/internal/shared/version.Commit=${NYARELAY_COMMIT} -X nyarelay/internal/shared/version.BuildDate=${NYARELAY_BUILD_DATE} -X nyarelay/internal/shared/version.UpdatePublicKey=${UPDATE_PUBLIC_KEY}" \
    && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o /out/nyarelay-controller ./cmd/controller
RUN --mount=type=secret,id=nyarelay_update_public_key,required=false \
    if [ -s /run/secrets/nyarelay_update_public_key ]; then \
      UPDATE_PUBLIC_KEY="$(tr -d '[:space:]' < /run/secrets/nyarelay_update_public_key)"; \
    else \
      UPDATE_PUBLIC_KEY="$(printf '%s' "$NYARELAY_UPDATE_PUBLIC_KEY" | tr -d '[:space:]')"; \
    fi \
    && LDFLAGS="-X nyarelay/internal/shared/version.Version=${NYARELAY_VERSION} -X nyarelay/internal/shared/version.Commit=${NYARELAY_COMMIT} -X nyarelay/internal/shared/version.BuildDate=${NYARELAY_BUILD_DATE} -X nyarelay/internal/shared/version.UpdatePublicKey=${UPDATE_PUBLIC_KEY}" \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o /out/nodes/nyarelay-node-linux-amd64 ./cmd/node
RUN --mount=type=secret,id=nyarelay_update_public_key,required=false \
    if [ -s /run/secrets/nyarelay_update_public_key ]; then \
      UPDATE_PUBLIC_KEY="$(tr -d '[:space:]' < /run/secrets/nyarelay_update_public_key)"; \
    else \
      UPDATE_PUBLIC_KEY="$(printf '%s' "$NYARELAY_UPDATE_PUBLIC_KEY" | tr -d '[:space:]')"; \
    fi \
    && LDFLAGS="-X nyarelay/internal/shared/version.Version=${NYARELAY_VERSION} -X nyarelay/internal/shared/version.Commit=${NYARELAY_COMMIT} -X nyarelay/internal/shared/version.BuildDate=${NYARELAY_BUILD_DATE} -X nyarelay/internal/shared/version.UpdatePublicKey=${UPDATE_PUBLIC_KEY}" \
    && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o /out/nodes/nyarelay-node-linux-arm64 ./cmd/node
RUN --mount=type=secret,id=nyarelay_update_signing_key,required=false \
    --mount=type=secret,id=nyarelay_update_public_key,required=false \
    if [ -s /run/secrets/nyarelay_update_public_key ]; then \
      UPDATE_PUBLIC_KEY="$(tr -d '[:space:]' < /run/secrets/nyarelay_update_public_key)"; \
    else \
      UPDATE_PUBLIC_KEY="$(printf '%s' "$NYARELAY_UPDATE_PUBLIC_KEY" | tr -d '[:space:]')"; \
    fi; \
    if [ -s /run/secrets/nyarelay_update_signing_key ]; then \
      if [ -z "$UPDATE_PUBLIC_KEY" ]; then \
        echo "NYARELAY_UPDATE_PUBLIC_KEY is required when NYARELAY_UPDATE_SIGNING_KEY is configured" >&2; \
        exit 1; \
      fi; \
      go run ./cmd/release-manifest \
        --node-dir /out/nodes \
        --version "${NYARELAY_VERSION}" \
        --commit "${NYARELAY_COMMIT}" \
        --build-date "${NYARELAY_BUILD_DATE}" \
        --private-key-file /run/secrets/nyarelay_update_signing_key \
        --expected-public-key "${UPDATE_PUBLIC_KEY}" \
        --manifest /out/nodes/node-release-manifest.json \
        --signature /out/nodes/node-release-manifest.sig \
        --public-key /out/nodes/node-release-public.key; \
    else \
      go run ./cmd/release-manifest \
        --node-dir /out/nodes \
        --version "${NYARELAY_VERSION}" \
        --commit "${NYARELAY_COMMIT}" \
        --build-date "${NYARELAY_BUILD_DATE}" \
        --manifest /out/nodes/node-release-manifest.json; \
    fi
RUN gzip -k -9 /out/nodes/nyarelay-node-linux-amd64 /out/nodes/nyarelay-node-linux-arm64
RUN cp "/out/nodes/nyarelay-node-${TARGETOS}-${TARGETARCH}" /out/nyarelay-node

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
RUN apk add --no-cache su-exec \
    && addgroup -S nyarelay \
    && adduser -S -G nyarelay nyarelay
WORKDIR /app
COPY --from=go-build /out/nyarelay-controller /usr/local/bin/nyarelay-controller
COPY --from=go-build /out/nyarelay-node /usr/local/bin/nyarelay-node
COPY --from=go-build /out/nodes /usr/local/lib/nyarelay
COPY --from=web /src/.tmp-webdist /app/.tmp-webdist
COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 8080
ENV NYARELAY_DATA=/data
ENV NYARELAY_NODE_BINARY=/usr/local/bin/nyarelay-node
ENV NYARELAY_NODE_BINARY_DIR=/usr/local/lib/nyarelay
ENTRYPOINT ["/entrypoint.sh"]
