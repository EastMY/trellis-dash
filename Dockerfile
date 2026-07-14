FROM node:26-alpine AS frontend-builder
WORKDIR /src/frontend
RUN npm install --global pnpm@10.33.0
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm build

FROM golang:1.26-alpine AS backend-builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /src/frontend/dist ./internal/webui/dist
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/trellis-dashboard ./cmd/trellis-dashboard

FROM alpine:3.23
RUN apk add --no-cache ca-certificates git tzdata \
    && addgroup -S dashboard -g 10001 \
    && adduser -S dashboard -G dashboard -u 10001 \
    && mkdir -p /data \
    && chown dashboard:dashboard /data
COPY --from=backend-builder /out/trellis-dashboard /usr/local/bin/trellis-dashboard
USER dashboard
EXPOSE 7465
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:7465/healthz >/dev/null || exit 1
ENTRYPOINT ["trellis-dashboard", "serve"]
CMD ["--host", "0.0.0.0", "--database", "/data/dashboard.db"]
