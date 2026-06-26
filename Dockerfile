FROM node:24-alpine AS web
WORKDIR /src/web/app
COPY web/app/package.json web/app/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/app ./
RUN npm run build

FROM golang:1.26-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/nyarelay-controller ./cmd/controller
RUN go build -o /out/nyarelay-node ./cmd/node

FROM alpine:3.22
RUN apk add --no-cache su-exec \
    && addgroup -S nyarelay \
    && adduser -S -G nyarelay nyarelay
WORKDIR /app
COPY --from=go-build /out/nyarelay-controller /usr/local/bin/nyarelay-controller
COPY --from=go-build /out/nyarelay-node /usr/local/bin/nyarelay-node
COPY --from=web /src/.tmp-webdist /app/.tmp-webdist
COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
EXPOSE 8080
VOLUME ["/data"]
ENV NYARELAY_DATA=/data
ENTRYPOINT ["/entrypoint.sh"]
