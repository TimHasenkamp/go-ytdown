# Error Reporting System

Dieses Projekt verfügt über ein automatisches Error-Reporting System, das bei Fehlern detaillierte Reports an Slack sendet.

## Setup

### 1. Slack Webhook erstellen

1. Gehe zu https://api.slack.com/messaging/webhooks
2. Klicke auf "Create your Slack app"
3. Wähle "From scratch"
4. Gib deiner App einen Namen (z.B. "YouTube Downloader Errors")
5. Wähle deinen Workspace
6. Gehe zu "Incoming Webhooks" und aktiviere es
7. Klicke "Add New Webhook to Workspace"
8. Wähle den Channel aus (z.B. #errors oder #alerts)
9. Kopiere die Webhook URL (Format: `https://hooks.slack.com/services/XXX/YYY/ZZZ`)

### 2. Webhook URL konfigurieren

#### Auf dem Server (Production):

```bash
# Erstelle .env Datei
cd /opt/ytdownloader
nano .env

# Füge hinzu:
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/YOUR/WEBHOOK/URL
```

Oder setze die Variable direkt in der Shell:

```bash
export SLACK_WEBHOOK_URL="https://hooks.slack.com/services/YOUR/WEBHOOK/URL"
```

Dann starte den Container neu:

```bash
docker compose down
docker compose up -d
```

#### Lokal (Development):

```bash
# Kopiere .env.example zu .env
cp .env.example .env

# Editiere .env und füge deine Webhook URL ein
nano .env

# Starte mit docker-compose
docker compose up -d
```

## Was wird gemeldet?

### Backend-Fehler
- Download-Fehler
- YouTube URL-Probleme
- Datei-Zugriffsfehler

### Frontend-Fehler
- JavaScript Runtime-Fehler
- Unhandled Promise Rejections
- SSE (Server-Sent Events) Verbindungsfehler
- Download-Fehler

## Slack-Benachrichtigung enthält:

- **Error Message**: Die Fehlermeldung
- **URL**: Welche Seite war der User gerade
- **Timestamp**: Wann ist der Fehler aufgetreten
- **User Agent**: Browser und OS des Users
- **Session ID**: Eindeutige Session für Tracking
- **Browser Info**: Name, Version, OS, Screen-Auflösung
- **Stack Trace**: Technischer Error-Stack (falls verfügbar)
- **Last Actions**: Die letzten 10 User-Aktionen vor dem Fehler

Beispiel:
```
🚨 YouTube Downloader Error Report

Error Message: SSE Connection Error
URL: https://music.hasenkamp.dev
Timestamp: 2025-11-08T19:30:45.123Z
User Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64)...
Session ID: abc123xyz
Browser: Chrome 120.0 on Windows

Stack Trace:
```
Error at line 382 in App.jsx
...
```

Last Actions:
1. [2025-11-08T19:30:20] Format changed: mp3 → mp4
2. [2025-11-08T19:30:25] Download initiated: format=mp4
3. [2025-11-08T19:30:30] SSE connection started: session=xyz123
4. [2025-11-08T19:30:45] SSE error: readyState=2
```

## Testen

Um zu testen, ob Error-Reporting funktioniert:

1. **Backend-Logs prüfen**:
```bash
docker compose logs -f | grep -i "slack\|error"
```

2. **Test-Error im Frontend auslösen** (Browser Console):
```javascript
throw new Error("Test Error Report")
```

3. **Check Slack Channel** - du solltest eine Benachrichtigung erhalten

## Deaktivieren

Falls du keine Benachrichtigungen möchtest:

```bash
# .env Datei löschen oder SLACK_WEBHOOK_URL leer lassen
unset SLACK_WEBHOOK_URL
docker compose restart
```

Ohne `SLACK_WEBHOOK_URL` werden Fehler nur in den Docker Logs gespeichert, aber nicht an Slack gesendet.

## Logs

Alle Fehler werden zusätzlich in den Docker-Logs gespeichert:

```bash
# Alle Error-Reports anzeigen
docker compose logs | grep "\[ErrorReport\]"

# Live-Monitoring
docker compose logs -f --tail=100
```
