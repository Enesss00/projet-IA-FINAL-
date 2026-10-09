# syntax=docker/dockerfile:1
# PIT WALL — multi-stage build: frontend (Node) → binary (Go) → distroless.

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN mkdir -p ../server/internal/webui/dist && npm run build

FROM golang:1.24-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pitwall ./cmd/pitwall \
 && /out/pitwall verify -seeds 1 -sims 300 -workers 8

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/pitwall /pitwall
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/pitwall", "serve"]
