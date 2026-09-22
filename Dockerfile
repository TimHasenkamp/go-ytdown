# Stage 1: React Frontend Build
# Node 20+ ist Pflicht: Vite 8 verlangt ^20.19.0 || >=22.12.0.
# Node 18 ist ausserdem seit April 2025 End-of-Life.
FROM node:22-alpine AS frontend-builder

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
# Die Stage ist benannt, damit die CI sie per --no-cache-filter=runtime gezielt
# ohne Cache bauen kann. Sonst wird die pip-Layer wiederverwendet und yt-dlp
# bleibt auf dem Stand des ersten Builds stehen.
FROM alpine:latest AS runtime

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
    && pip3 install --break-system-packages --no-cache-dir "yt-dlp[default]" \
    && pip3 install --break-system-packages --no-cache-dir bgutil-ytdlp-pot-provider

# Build-Gate: bricht den Build ab, wenn yt-dlp zu alt ist. Greift vor allem dann,
# wenn die Layer oben doch aus dem Cache kam - ein veraltetes yt-dlp fuehrt bei
# YouTube zu "HTTP Error 403: Forbidden".
RUN yt-dlp --version && python3 -c "\
import datetime, subprocess, sys;\
v = subprocess.check_output(['yt-dlp', '--version']).decode().strip();\
released = datetime.date(*map(int, v.split('.')[:3]));\
age = (datetime.date.today() - released).days;\
print('yt-dlp %s ist %d Tage alt' % (v, age));\
sys.exit('FEHLER: yt-dlp ist veraltet - Build ohne Cache wiederholen' if age > 90 else 0)"

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
RUN mkdir -p /app/downloads && \
    chown -R appuser:appgroup /app

# Note: Running as root to avoid permission issues with Docker volume mounts
# Downloads are temporary and deleted after serving, so security impact is minimal
# USER appuser  # Commented out for now - re-enable if volume mount permissions are fixed

# Exponiere Port
EXPOSE 8080

# Healthcheck
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

# Starte die Anwendung
CMD ["./ytdownloader"]
