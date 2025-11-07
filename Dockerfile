# Stage 1: React Frontend Build
FROM node:18-alpine AS frontend-builder

WORKDIR /frontend

# Kopiere package files
COPY frontend/package*.json ./

# Install dependencies
RUN npm install

# Kopiere Frontend Source
COPY frontend/ ./

# Build React App
RUN npm run build

# Stage 2: Go Backend Build
FROM golang:1.21-alpine AS backend-builder

WORKDIR /app

# Kopiere Go-Module-Dateien
COPY go.mod go.sum* ./

# Download Dependencies (falls go.sum existiert)
RUN go mod download || true

# Kopiere Source Code
COPY *.go ./

# Build der Anwendung
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ytdownloader .

# Stage 3: Runtime
FROM alpine:latest

# Installiere Runtime-Abhängigkeiten
RUN apk add --no-cache \
    python3 \
    py3-pip \
    git \
    ffmpeg \
    ca-certificates \
    wget \
    curl \
    unzip \
    nodejs \
    && pip3 install --break-system-packages --no-cache-dir --upgrade pip \
    && pip3 install --break-system-packages --no-cache-dir git+https://github.com/yt-dlp/yt-dlp.git@master \
    && pip3 install --break-system-packages --no-cache-dir bgutil-ytdlp-pot-provider

# Erstelle non-root User
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Make deno available for appuser
ENV PATH="/usr/local/bin:$PATH"

WORKDIR /app

# Kopiere Binary aus Backend-Build-Stage
COPY --from=backend-builder /app/ytdownloader .

# Kopiere React Build aus Frontend-Build-Stage
COPY --from=frontend-builder /frontend/build ./static/

# Erstelle downloads Verzeichnis
RUN mkdir -p /app/downloads && chown -R appuser:appgroup /app

# Wechsle zu non-root User
USER appuser

# Exponiere Port
EXPOSE 8080

# Healthcheck
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

# Starte die Anwendung
CMD ["./ytdownloader"]
