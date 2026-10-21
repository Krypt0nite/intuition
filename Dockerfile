# Single image: Go binary serving API + WebSockets + the built frontend.
FROM node:20-alpine AS web
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.22-alpine AS api
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY content /src/content
RUN go run ./cmd/content validate -dir /src/content \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/content ./cmd/content

FROM alpine:3.20
RUN adduser -D -H app
WORKDIR /app
COPY --from=api /out/server /out/content /app/
COPY content /app/content
COPY --from=web /src/frontend/dist /app/web
ENV PORT=8080 CONTENT_DIR=/app/content STATIC_DIR=/app/web APP_ENV=production
USER app
EXPOSE 8080
CMD ["/app/server"]
