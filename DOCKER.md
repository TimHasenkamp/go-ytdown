# Docker Quick Start Guide

## 🚀 Schnellstart

```bash
# Container starten
docker-compose up -d

# Browser öffnen
# http://localhost:8080
```

## 📝 Wichtige Befehle

### Container Management

```bash
# Container starten (im Hintergrund)
docker-compose up -d

# Container stoppen
docker-compose down

# Container neu starten
docker-compose restart

# Status überprüfen
docker-compose ps
```

### Logs & Debugging

```bash
# Logs anzeigen
docker-compose logs

# Logs live verfolgen
docker-compose logs -f

# Nur die letzten 100 Zeilen
docker-compose logs --tail=100
```

### Build & Updates

```bash
# Container neu bauen
docker-compose build

# Neu bauen und starten
docker-compose up -d --build

# Cache ignorieren (kompletter Rebuild)
docker-compose build --no-cache
```

### Daten & Volumes

```bash
# Downloads-Ordner anzeigen
ls -la downloads/

# Downloads löschen
rm -rf downloads/*

# In den Container einsteigen (für Debugging)
docker-compose exec ytdownloader sh
```

## 🔧 Konfiguration

### Port ändern

In [docker-compose.yml](docker-compose.yml) ändern:

```yaml
ports:
  - "3000:8080"  # Ändere 3000 zu deinem gewünschten Port
```

### Zeitzone ändern

In [docker-compose.yml](docker-compose.yml) ändern:

```yaml
environment:
  - TZ=Europe/Vienna  # Oder deine Zeitzone
```

### Downloads-Ordner ändern

In [docker-compose.yml](docker-compose.yml) ändern:

```yaml
volumes:
  - /dein/eigener/pfad:/app/downloads
```

## 🐛 Troubleshooting

### Container startet nicht

```bash
# Logs überprüfen
docker-compose logs

# Container entfernen und neu starten
docker-compose down
docker-compose up -d
```

### Port bereits belegt

```bash
# Welcher Prozess nutzt Port 8080?
# Windows
netstat -ano | findstr :8080

# Linux/macOS
lsof -i :8080
```

Dann entweder den Port in docker-compose.yml ändern oder den anderen Prozess beenden.

### Downloads funktionieren nicht

```bash
# Berechtigungen überprüfen
ls -la downloads/

# Bei Berechtigungsproblemen
chmod 755 downloads/
```

### Image neu bauen nach Code-Änderungen

```bash
# Stoppen, neu bauen, starten
docker-compose down
docker-compose build --no-cache
docker-compose up -d
```

## 📊 Ressourcen überwachen

```bash
# Container-Statistiken
docker stats ytdownloader

# Speichernutzung
docker system df
```

## 🧹 Aufräumen

```bash
# Container und Netzwerke entfernen
docker-compose down

# Zusätzlich Volumes entfernen (löscht Downloads!)
docker-compose down -v

# Alle ungenutzten Docker-Ressourcen entfernen
docker system prune -a
```

## 💡 Tipps

- **Entwicklung**: Nutze `docker-compose logs -f` um Fehler zu sehen
- **Produktion**: Der Container startet automatisch neu (`restart: unless-stopped`)
- **Backups**: Sichere regelmäßig den `downloads/` Ordner
- **Updates**: Bei yt-dlp Updates Image neu bauen: `docker-compose build --no-cache`

## 🔍 Weitere Infos

- [Dockerfile](Dockerfile) - Image-Definition
- [docker-compose.yml](docker-compose.yml) - Container-Konfiguration
- [README.md](README.md) - Vollständige Dokumentation
