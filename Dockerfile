FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web/app
COPY web/app/package.json web/app/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/app ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -buildvcs=false -o /out/nyarelay-controller ./cmd/controller
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -o /out/nodes/nyarelay-node-linux-amd64 ./cmd/node
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -o /out/nodes/nyarelay-node-linux-arm64 ./cmd/node
RUN gzip -k -9 /out/nodes/nyarelay-node-linux-amd64 /out/nodes/nyarelay-node-linux-arm64
RUN cp "/out/nodes/nyarelay-node-${TARGETOS}-${TARGETARCH}" /out/nyarelay-node

FROM alpine:3.22
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
VOLUME ["/data"]
ENV NYARELAY_DATA=/data
ENV NYARELAY_NODE_BINARY=/usr/local/bin/nyarelay-node
ENV NYARELAY_NODE_BINARY_DIR=/usr/local/lib/nyarelay
ENTRYPOINT ["/entrypoint.sh"]
