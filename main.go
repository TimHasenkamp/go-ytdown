package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DownloadRequest struct {
	URL    string `json:"url"`
	Format string `json:"format"`
}

type DownloadResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
}

type ProgressUpdate struct {
	Progress int    `json:"progress"`
	Status   string `json:"status"`
	Error    bool   `json:"error,omitempty"` // Indicates if this is an error message
}

type FormatCheckResponse struct {
	Success        bool     `json:"success"`
	Message        string   `json:"message,omitempty"`
	HasSABR        bool     `json:"hasSABR"`
	BestVideoInfo  string   `json:"bestVideoInfo,omitempty"`
	BestAudioInfo  string   `json:"bestAudioInfo,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	SelectedFormat string   `json:"selectedFormat,omitempty"`
}

type ResolveRequest struct {
	URL string `json:"url"`
}

type ResolveResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message,omitempty"`
	OriginalURL  string `json:"originalUrl"`
	ResolvedURL  string `json:"resolvedUrl"`
	WasRedirect  bool   `json:"wasRedirect"`
	WasCanonical bool   `json:"wasCanonical"`
}

type ErrorReport struct {
	ErrorMessage string            `json:"errorMessage"`
	ErrorStack   string            `json:"errorStack"`
	URL          string            `json:"url"`
	UserAgent    string            `json:"userAgent"`
	Timestamp    string            `json:"timestamp"`
	SessionID    string            `json:"sessionId"`
	LastActions  []string          `json:"lastActions"`
	BrowserInfo  map[string]string `json:"browserInfo"`
}

type SlackMessage struct {
	Text        string              `json:"text,omitempty"`
	Blocks      []SlackBlock        `json:"blocks,omitempty"`
	Attachments []SlackAttachment   `json:"attachments,omitempty"`
}

type SlackBlock struct {
	Type string                 `json:"type"`
	Text *SlackText             `json:"text,omitempty"`
	Fields []SlackText          `json:"fields,omitempty"`
}

type SlackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type SlackAttachment struct {
	Color  string       `json:"color"`
	Fields []SlackField `json:"fields"`
}

type SlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

type CompletedDownload struct {
	FinalUpdate ProgressUpdate
	CompletedAt time.Time
}

var (
	progressClients      = make(map[string][]chan ProgressUpdate) // Multiple clients per session
	completedDownloads   = make(map[string]*CompletedDownload)    // Cache completed downloads for reconnect
	progressMutex        sync.RWMutex
	slackWebhookURL      = os.Getenv("SLACK_WEBHOOK_URL") // Set via environment variable
	completedCacheTTL    = 5 * time.Minute                 // Keep completed downloads for 5 minutes
)

func main() {
	// Serve static files
	http.Handle("/", http.FileServer(http.Dir("./static")))

	// Download endpoint
	http.HandleFunc("/download", handleDownload)
	http.HandleFunc("/progress", handleProgress)
	http.HandleFunc("/download-file/", handleDownloadFile)
	http.HandleFunc("/check-formats", handleCheckFormats)
	http.HandleFunc("/resolve", handleResolve)
	http.HandleFunc("/report-error", handleErrorReport)
	http.HandleFunc("/test-slack", handleTestSlack) // Test endpoint for Slack notifications

	// Check if yt-dlp is installed
	if err := checkYtDlp(); err != nil {
		log.Printf("Warning: yt-dlp not found. Please install it: %v", err)
	}

	// Send startup notification to Slack
	go sendStartupNotification()

	// Start cleanup goroutine for old completed downloads
	go cleanupCompletedDownloads()

	port := "8080"
	log.Printf("Server starting on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func checkYtDlp() error {
	cmd := exec.Command("yt-dlp", "--version")
	return cmd.Run()
}

// removeEmojis removes all emoji characters from a string
func removeEmojis(s string) string {
	// Regex to match emoji characters
	emojiPattern := regexp.MustCompile(`[\x{1F600}-\x{1F64F}]|[\x{1F300}-\x{1F5FF}]|[\x{1F680}-\x{1F6FF}]|[\x{1F700}-\x{1F77F}]|[\x{1F780}-\x{1F7FF}]|[\x{1F800}-\x{1F8FF}]|[\x{1F900}-\x{1F9FF}]|[\x{1FA00}-\x{1FA6F}]|[\x{1FA70}-\x{1FAFF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]`)
	return emojiPattern.ReplaceAllString(s, "")
}

// sanitizeFilename removes emojis and problematic characters from filename
func sanitizeFilename(filename string) string {
	// Remove emojis
	filename = removeEmojis(filename)

	// Replace problematic characters with underscores
	problematicChars := regexp.MustCompile(`[<>:"|?*｜]`)
	filename = problematicChars.ReplaceAllString(filename, "_")

	// Trim whitespace and dots
	filename = strings.TrimSpace(filename)
	filename = strings.Trim(filename, ".")

	// Collapse multiple spaces/underscores
	multiSpace := regexp.MustCompile(`\s+`)
	filename = multiSpace.ReplaceAllString(filename, " ")
	multiUnderscore := regexp.MustCompile(`_+`)
	filename = multiUnderscore.ReplaceAllString(filename, "_")

	return filename
}

// isValidYouTubeURL validates that the URL is from YouTube (including all variants and mobile)
func isValidYouTubeURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	host := strings.ToLower(parsed.Host)

	// Remove www. prefix for comparison
	host = strings.TrimPrefix(host, "www.")

	// List of valid YouTube domains
	validHosts := []string{
		"youtube.com",
		"m.youtube.com",
		"youtu.be",
		"youtube-nocookie.com",
	}

	// Check if host matches or is a subdomain of YouTube
	for _, validHost := range validHosts {
		if host == validHost || strings.HasSuffix(host, "."+validHost) {
			return true
		}
	}

	return false
}

// resolveHTTP follows HTTP redirects manually (HEAD first, then GET fallback)
// and returns the final URL after up to maxHops hops.
func resolveHTTP(start string, maxHops int) (string, error) {
	u := start
	client := &http.Client{
		Timeout: 15 * time.Second,
		// do NOT auto-follow; we want to read Location ourselves
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for i := 0; i < maxHops; i++ {
		req, err := http.NewRequest(http.MethodHead, u, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "yt-url-resolver/1.0 (+https://example.local)")

		resp, err := client.Do(req)
		if err != nil {
			// Some servers don't like HEAD; try GET
			req.Method = http.MethodGet
			resp, err = client.Do(req)
			if err != nil {
				return "", err
			}
		}
		resp.Body.Close()

		// 3xx → follow Location
		if resp.StatusCode/100 == 3 {
			loc := resp.Header.Get("Location")
			if loc == "" {
				return "", errors.New("redirect without Location header")
			}
			// Resolve relative locations
			next, err := url.Parse(loc)
			if err != nil {
				return "", err
			}
			base, _ := url.Parse(u)
			u = base.ResolveReference(next).String()
			continue
		}

		// Non-redirect → done
		return u, nil
	}
	return "", fmt.Errorf("too many redirects (>%d)", maxHops)
}

// canonicalYouTube normalizes many YouTube URL shapes into https://www.youtube.com/watch?v=ID
// Keeps only v and optionally t (timestamp) query params.
func canonicalYouTube(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	host := strings.ToLower(parsed.Host)
	// unify host
	if host == "youtu.be" {
		// Path is /VIDEO_ID
		id := strings.TrimPrefix(parsed.Path, "/")
		if id == "" {
			return "", false
		}
		// keep optional t=… from short URL
		t := parsed.Query().Get("t")
		q := url.Values{}
		q.Set("v", id)
		if t != "" {
			q.Set("t", t)
		}
		return (&url.URL{
			Scheme:   "https",
			Host:     "www.youtube.com",
			Path:     "/watch",
			RawQuery: q.Encode(),
		}).String(), true
	}

	if strings.HasSuffix(host, "youtube.com") || strings.HasSuffix(host, "youtube-nocookie.com") || strings.HasSuffix(host, "m.youtube.com") {
		// shorts/live → watch
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 && (parts[0] == "shorts" || parts[0] == "live") {
			id := parts[1]
			if id != "" {
				q := url.Values{}
				q.Set("v", id)
				t := parsed.Query().Get("t")
				if t != "" {
					q.Set("t", t)
				}
				return (&url.URL{
					Scheme:   "https",
					Host:     "www.youtube.com",
					Path:     "/watch",
					RawQuery: q.Encode(),
				}).String(), true
			}
		}

		// already a watch URL?
		if strings.HasPrefix(parsed.Path, "/watch") {
			q := parsed.Query()
			id := q.Get("v")
			if id == "" {
				return "", false
			}
			// rebuild with only v and optional t
			only := url.Values{}
			only.Set("v", id)
			if t := q.Get("t"); t != "" {
				only.Set("t", t)
			}
			return (&url.URL{
				Scheme:   "https",
				Host:     "www.youtube.com",
				Path:     "/watch",
				RawQuery: only.Encode(),
			}).String(), true
		}

		// youtu.be embed-like: /embed/ID
		if strings.HasPrefix(parsed.Path, "/embed/") {
			id := path.Base(parsed.Path)
			if id != "" {
				q := url.Values{}
				q.Set("v", id)
				if t := parsed.Query().Get("start"); t != "" {
					// embed uses start=seconds; map to t
					q.Set("t", t+"s")
				}
				return (&url.URL{
					Scheme:   "https",
					Host:     "www.youtube.com",
					Path:     "/watch",
					RawQuery: q.Encode(),
				}).String(), true
			}
		}
	}

	return "", false
}

// resolveYouTubeURL combines canonicalization and HTTP redirect resolution
func resolveYouTubeURL(input string) (string, bool, bool, error) {
	// First: try canonicalize without network (works for youtu.be, shorts, etc.)
	if canon, ok := canonicalYouTube(input); ok {
		return canon, false, true, nil
	}

	// Otherwise: resolve HTTP redirects, then try canonicalize again.
	final, err := resolveHTTP(input, 10)
	if err != nil {
		// if redirect resolving failed, still return what we have
		return input, false, false, err
	}

	wasRedirect := final != input

	if canon, ok := canonicalYouTube(final); ok {
		return canon, wasRedirect, true, nil
	}

	// Fallback: return the final resolved URL
	return final, wasRedirect, false, nil
}

func handleResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ResolveResponse{
			Success: false,
			Message: "Ungültige Anfrage",
		})
		return
	}

	if req.URL == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ResolveResponse{
			Success: false,
			Message: "URL fehlt",
		})
		return
	}

	// Validate that URL is from YouTube
	if !isValidYouTubeURL(req.URL) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ResolveResponse{
			Success: false,
			Message: "Nur YouTube URLs sind erlaubt",
		})
		return
	}

	resolvedURL, wasRedirect, wasCanonical, err := resolveYouTubeURL(req.URL)

	response := ResolveResponse{
		Success:      true,
		OriginalURL:  req.URL,
		ResolvedURL:  resolvedURL,
		WasRedirect:  wasRedirect,
		WasCanonical: wasCanonical,
	}

	if err != nil {
		response.Message = fmt.Sprintf("Warnung: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// cleanURL entfernt Playlist-Parameter und andere unerwünschte URL-Teile
// Now uses the advanced resolver functionality
func cleanURL(rawURL string) (string, error) {
	// Use the resolver to canonicalize and clean the URL
	resolvedURL, _, _, err := resolveYouTubeURL(rawURL)
	if err != nil {
		// If resolution fails, fall back to basic parsing
		parsedURL, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			return "", parseErr
		}
		return parsedURL.String(), nil
	}

	return resolvedURL, nil
}

func handleProgress(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		log.Printf("[SSE] ERROR: No session ID provided")
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	log.Printf("[SSE] Client connected for session: %s", sessionID)

	// Server-Sent Events Headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Check if this download was already completed
	progressMutex.RLock()
	completed, wasCompleted := completedDownloads[sessionID]
	progressMutex.RUnlock()

	if wasCompleted {
		// Send the final update immediately and close
		log.Printf("[SSE] Reconnect to completed session %s, sending final update", sessionID)
		data, _ := json.Marshal(completed.FinalUpdate)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Create a new channel for this client
	progressChan := make(chan ProgressUpdate, 10)

	progressMutex.Lock()
	progressClients[sessionID] = append(progressClients[sessionID], progressChan)
	clientCount := len(progressClients[sessionID])
	progressMutex.Unlock()

	log.Printf("[SSE] Client connected for session %s (total clients: %d)", sessionID, clientCount)

	// Clean up on disconnect - remove this channel from the list
	defer func() {
		progressMutex.Lock()
		clients := progressClients[sessionID]
		for i, ch := range clients {
			if ch == progressChan {
				// Remove this channel from the slice
				progressClients[sessionID] = append(clients[:i], clients[i+1:]...)
				close(ch)
				log.Printf("[SSE] Client disconnected from session %s (remaining: %d)", sessionID, len(progressClients[sessionID]))

				// If no more clients, remove session entirely
				if len(progressClients[sessionID]) == 0 {
					delete(progressClients, sessionID)
					log.Printf("[SSE] All clients disconnected, removed session: %s", sessionID)
				}
				break
			}
		}
		progressMutex.Unlock()
	}()

	// Send updates to client
	updateCount := 0
	for update := range progressChan {
		updateCount++
		data, _ := json.Marshal(update)
		log.Printf("[SSE] Sending update #%d to session %s: %d%% - %s", updateCount, sessionID, update.Progress, update.Status)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	log.Printf("[SSE] Finished sending %d updates for session: %s", updateCount, sessionID)
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONResponse(w, DownloadResponse{
			Success: false,
			Message: "Ungültige Anfrage. Bitte versuche es erneut.",
		})
		return
	}

	// Validate URL
	if req.URL == "" {
		sendJSONResponse(w, DownloadResponse{
			Success: false,
			Message: "Bitte gib eine YouTube-URL ein.",
		})
		return
	}

	// Validate that URL is from YouTube
	if !isValidYouTubeURL(req.URL) {
		sendJSONResponse(w, DownloadResponse{
			Success: false,
			Message: "Nur YouTube URLs sind erlaubt. Bitte verwende einen gültigen YouTube-Link.",
		})
		return
	}

	// Clean URL (remove playlist parameters)
	cleanedURL, err := cleanURL(req.URL)
	if err != nil {
		sendJSONResponse(w, DownloadResponse{
			Success: false,
			Message: "Ungültige URL. Bitte überprüfe den YouTube-Link.",
		})
		return
	}

	// Validate that it's a YouTube URL
	if !strings.Contains(cleanedURL, "youtube.com") && !strings.Contains(cleanedURL, "youtu.be") {
		sendJSONResponse(w, DownloadResponse{
			Success: false,
			Message: "Nur YouTube-URLs werden unterstützt.",
		})
		return
	}

	// Validate format
	validFormats := map[string]bool{
		"mp4": true,
		"mp3": true,
		"wav": true,
		"m4a": true,
	}
	if !validFormats[req.Format] {
		sendJSONResponse(w, DownloadResponse{
			Success: false,
			Message: "Ungültiges Format ausgewählt.",
		})
		return
	}

	// Generate session ID
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Download the video in goroutine
	go func() {
		filename, err := downloadVideo(cleanedURL, req.Format, sessionID)
		if err != nil {
			log.Printf("Download error: %v", err)
			sendError(sessionID, fmt.Sprintf("%v", err))
		} else {
			sendProgress(sessionID, 100, fmt.Sprintf("Completed: %s", filename))
		}
	}()

	sendJSONResponse(w, DownloadResponse{
		Success:  true,
		Message:  sessionID,
		Filename: sessionID,
	})
}

func sendProgress(sessionID string, progress int, status string) {
	log.Printf("Progress [%s]: %d%% - %s", sessionID, progress, status)

	update := ProgressUpdate{Progress: progress, Status: status, Error: false}

	progressMutex.RLock()
	clients := progressClients[sessionID]
	progressMutex.RUnlock()

	// Send to all connected clients for this session
	for _, ch := range clients {
		select {
		case ch <- update:
		default:
			// Channel full or closed, skip
		}
	}

	// If 100%, close all channels and cache the final update
	if progress == 100 {
		progressMutex.Lock()
		for _, ch := range progressClients[sessionID] {
			close(ch)
		}
		delete(progressClients, sessionID)

		// Cache the final update for reconnects
		completedDownloads[sessionID] = &CompletedDownload{
			FinalUpdate: update,
			CompletedAt: time.Now(),
		}

		progressMutex.Unlock()
		log.Printf("[SSE] Closed all channels for completed session: %s", sessionID)
	}
}

func sendError(sessionID string, errorMsg string) {
	log.Printf("Error [%s]: %s", sessionID, errorMsg)

	update := ProgressUpdate{Progress: -1, Status: errorMsg, Error: true}

	progressMutex.Lock()
	clients := progressClients[sessionID]

	// Send error to all connected clients
	for _, ch := range clients {
		select {
		case ch <- update:
		default:
			// Channel full or closed, skip
		}
	}

	// Close all channels and cache the error for reconnects
	for _, ch := range clients {
		close(ch)
	}
	delete(progressClients, sessionID)

	// Cache the error update for reconnects
	completedDownloads[sessionID] = &CompletedDownload{
		FinalUpdate: update,
		CompletedAt: time.Now(),
	}

	progressMutex.Unlock()

	log.Printf("[SSE] Closed all channels for errored session: %s", sessionID)
}

func downloadVideo(url, format, sessionID string) (string, error) {
	// Create downloads directory if it doesn't exist
	downloadsDir := "./downloads"
	if err := os.MkdirAll(downloadsDir, 0755); err != nil {
		return "", fmt.Errorf("Fehler beim Erstellen des Download-Verzeichnisses")
	}

	sendProgress(sessionID, 10, "Download wird gestartet...")

	// Generate timestamp for unique filename
	timestamp := time.Now().Format("20060102_150405")
	outputTemplate := filepath.Join(downloadsDir, fmt.Sprintf("%s_%%(title)s.%%(ext)s", timestamp))

	var args []string

	// Common args for all formats
	commonArgs := []string{
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"--no-playlist",
	}

	switch format {
	case "mp4":
		args = append(commonArgs,
			"-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
			"--merge-output-format", "mp4",
			"-o", outputTemplate,
			url,
		)
	case "mp3":
		args = append(commonArgs,
			"-x",
			"--audio-format", "mp3",
			"--audio-quality", "0",
			"-o", outputTemplate,
			url,
		)
	case "wav":
		args = append(commonArgs,
			"-x",
			"--audio-format", "wav",
			"-o", outputTemplate,
			url,
		)
	case "m4a":
		args = append(commonArgs,
			"-x",
			"--audio-format", "m4a",
			"--audio-quality", "0",
			"-o", outputTemplate,
			url,
		)
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}

	sendProgress(sessionID, 20, "Video-Informationen werden abgerufen...")

	cmd := exec.Command("yt-dlp", args...)

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("Fehler beim Starten des Downloads")
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("Fehler beim Starten des Downloads")
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("Download konnte nicht gestartet werden")
	}

	// Collect stderr output for better error messages
	var stderrOutput strings.Builder

	// Monitor stdout for progress (yt-dlp writes download progress to stdout!)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			// Log stdout for debugging
			if line != "" {
				log.Printf("yt-dlp stdout: %s", line)
			}

			// Parse download progress from stdout
			// Format: "[download]  45.3% of 10.00MiB at  500.00KiB/s ETA 00:20"
			if strings.Contains(line, "[download]") && strings.Contains(line, "%") {
				parts := strings.Fields(line)
				for i, part := range parts {
					if strings.HasSuffix(part, "%") {
						percentStr := strings.TrimSuffix(part, "%")
						if percent, err := strconv.ParseFloat(percentStr, 64); err == nil {
							// Scale: 20-90% range for download phase
							scaledProgress := 20 + int(percent*0.7)
							if scaledProgress > 90 {
								scaledProgress = 90
							}
							sendProgress(sessionID, scaledProgress, fmt.Sprintf("Download läuft... %.1f%%", percent))
							break
						}
					}
					if part == "100%" && i > 0 {
						sendProgress(sessionID, 90, "Download abgeschlossen")
						break
					}
				}
			} else if strings.Contains(line, "[ExtractAudio]") || strings.Contains(line, "Extracting audio") {
				sendProgress(sessionID, 92, "Audio wird extrahiert...")
			} else if strings.Contains(line, "[ffmpeg]") && strings.Contains(line, "Destination:") {
				sendProgress(sessionID, 95, "Wird konvertiert...")
			}
		}
	}()

	// Monitor stderr for errors AND progress (yt-dlp writes progress to stderr!)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrOutput.WriteString(line + "\n")
			log.Printf("yt-dlp: %s", line)

			// Parse download progress from stderr
			// Format: "[download]  45.3% of 10.00MiB at  500.00KiB/s ETA 00:20"
			if strings.Contains(line, "[download]") && strings.Contains(line, "%") {
				parts := strings.Fields(line)
				for i, part := range parts {
					if strings.HasSuffix(part, "%") {
						percentStr := strings.TrimSuffix(part, "%")
						if percent, err := strconv.ParseFloat(percentStr, 64); err == nil {
							// Scale: 20-90% range for download phase
							scaledProgress := 20 + int(percent*0.7)
							if scaledProgress > 90 {
								scaledProgress = 90
							}
							sendProgress(sessionID, scaledProgress, fmt.Sprintf("Download läuft... %.1f%%", percent))
							break
						}
					}
					if part == "100%" && i > 0 {
						sendProgress(sessionID, 90, "Download abgeschlossen")
						break
					}
				}
			} else if strings.Contains(line, "[ExtractAudio]") || strings.Contains(line, "Extracting audio") {
				sendProgress(sessionID, 92, "Audio wird extrahiert...")
			} else if strings.Contains(line, "[ffmpeg]") && strings.Contains(line, "Destination:") {
				sendProgress(sessionID, 95, "Wird konvertiert...")
			}
		}
	}()

	if err := cmd.Wait(); err != nil {
		errorMsg := stderrOutput.String()

		// Log full stderr for debugging
		log.Printf("[yt-dlp] Full stderr output for session %s:\n%s", sessionID, errorMsg)

		// Report to Slack for critical errors
		reportBackendError(fmt.Sprintf("yt-dlp failed: %v", err), map[string]string{
			"url":     url,
			"format":  format,
			"session": sessionID,
			"stderr":  truncateString(errorMsg, 1000), // Increased from 500 to 1000
		})

		// Check for specific error conditions
		if strings.Contains(errorMsg, "Requested format is not available") {
			return "", fmt.Errorf("Das gewählte Format ist für dieses Video nicht verfügbar. Versuche ein anderes Format.")
		}
		if strings.Contains(errorMsg, "Only images are available") {
			return "", fmt.Errorf("Dieses Video enthält nur Bilder und kann nicht heruntergeladen werden")
		}
		if strings.Contains(errorMsg, "Video unavailable") {
			return "", fmt.Errorf("Video ist nicht verfügbar oder wurde gelöscht")
		}
		if strings.Contains(errorMsg, "Private video") {
			return "", fmt.Errorf("Video ist privat und kann nicht heruntergeladen werden")
		}
		if strings.Contains(errorMsg, "This video is not available in your country") || strings.Contains(errorMsg, "geo") {
			return "", fmt.Errorf("Video ist in deinem Land nicht verfügbar (Geo-Blocking)")
		}
		if strings.Contains(errorMsg, "copyright") {
			return "", fmt.Errorf("Video ist urheberrechtlich geschützt und kann nicht heruntergeladen werden")
		}
		if strings.Contains(errorMsg, "Sign in") || strings.Contains(errorMsg, "age") {
			return "", fmt.Errorf("Video erfordert Altersbeschränkung oder Anmeldung")
		}
		if strings.Contains(errorMsg, "network") || strings.Contains(errorMsg, "connection") {
			return "", fmt.Errorf("Netzwerkfehler. Bitte überprüfe deine Internetverbindung")
		}
		if strings.Contains(errorMsg, "429") || strings.Contains(errorMsg, "Too Many Requests") {
			return "", fmt.Errorf("Zu viele Anfragen. Bitte versuche es in einigen Minuten erneut")
		}

		// Generic error if no specific match
		return "", fmt.Errorf("Download fehlgeschlagen. Bitte überprüfe die URL und versuche es erneut")
	}

	sendProgress(sessionID, 90, "Download abgeschlossen, finalisiere...")

	// Try to find the downloaded file
	files, err := filepath.Glob(filepath.Join(downloadsDir, timestamp+"_*"))
	if err != nil {
		return "", fmt.Errorf("Fehler beim Suchen der heruntergeladenen Datei")
	}

	if len(files) == 0 {
		return "", fmt.Errorf("Download abgeschlossen, aber Datei wurde nicht gefunden")
	}

	originalPath := files[0]
	originalFilename := filepath.Base(originalPath)

	// Sanitize filename to remove emojis and problematic characters
	sanitizedFilename := sanitizeFilename(originalFilename)

	// If filename changed, rename the file
	if sanitizedFilename != originalFilename {
		newPath := filepath.Join(downloadsDir, sanitizedFilename)
		if err := os.Rename(originalPath, newPath); err != nil {
			log.Printf("Warning: Could not rename file from %s to %s: %v", originalFilename, sanitizedFilename, err)
			// Continue with original filename if rename fails
			return originalFilename, nil
		}
		log.Printf("File renamed from %s to %s (emojis removed)", originalFilename, sanitizedFilename)
		return sanitizedFilename, nil
	}

	// Return just the filename (not the full path)
	return originalFilename, nil
}

func handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	// Extract filename from URL path
	filename := strings.TrimPrefix(r.URL.Path, "/download-file/")
	log.Printf("[Download] Request received for file: %s (raw path: %s)", filename, r.URL.Path)

	if filename == "" {
		log.Printf("[Download] ERROR: No filename provided")
		http.Error(w, "Dateiname fehlt", http.StatusBadRequest)
		return
	}

	// URL decode the filename
	decodedFilename, err := url.QueryUnescape(filename)
	if err != nil {
		log.Printf("[Download] ERROR: Failed to decode filename: %v", err)
		http.Error(w, "Ungültiger Dateiname", http.StatusBadRequest)
		return
	}
	filename = decodedFilename
	log.Printf("[Download] Decoded filename: %s", filename)

	// Security: Prevent directory traversal
	filename = filepath.Base(filename)
	log.Printf("[Download] After Base(): %s", filename)

	// Additional security: reject suspicious filenames
	if strings.Contains(filename, "..") || strings.ContainsAny(filename, "/\\") {
		log.Printf("[Download] SECURITY: Rejected suspicious filename: %s", filename)
		http.Error(w, "Ungültiger Dateiname", http.StatusBadRequest)
		return
	}

	// Build full path
	filePath := filepath.Join("./downloads", filename)
	log.Printf("[Download] Full path: %s", filePath)

	// Security: Verify the resolved path is still within downloads directory
	absDownloads, _ := filepath.Abs("./downloads")
	absFilePath, _ := filepath.Abs(filePath)
	if !strings.HasPrefix(absFilePath, absDownloads) {
		log.Printf("[Download] SECURITY: Path traversal attempt detected: %s", filename)
		http.Error(w, "Zugriff verweigert", http.StatusForbidden)
		return
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("[Download] ERROR: File not found: %s", filePath)
		// List available files for debugging
		files, _ := filepath.Glob("./downloads/*")
		log.Printf("[Download] Available files in downloads:")
		for _, f := range files {
			log.Printf("[Download]   - %s", filepath.Base(f))
		}
		http.Error(w, "Datei nicht gefunden. Möglicherweise wurde sie bereits heruntergeladen.", http.StatusNotFound)
		return
	}

	log.Printf("[Download] File found, preparing to send: %s", filename)

	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("Download file error: Cannot open file %s: %v", filename, err)
		http.Error(w, "Fehler beim Öffnen der Datei", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Get file info for size
	fileInfo, err := file.Stat()
	if err != nil {
		log.Printf("Download file error: Cannot get file info %s: %v", filename, err)
		http.Error(w, "Fehler beim Lesen der Dateiinformationen", http.StatusInternalServerError)
		return
	}

	// Set headers for download
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	// Stream file to browser
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("Error streaming file: %v", err)
		return
	}

	// Close file before deleting
	file.Close()

	// Delete file after successful download
	if err := os.Remove(filePath); err != nil {
		log.Printf("Error deleting file after download: %v", err)
	} else {
		log.Printf("File deleted after download: %s", filename)
	}
}

func handleCheckFormats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FormatCheckResponse{
			Success: false,
			Message: "Ungültige Anfrage",
		})
		return
	}

	// Validate that URL is from YouTube
	if !isValidYouTubeURL(req.URL) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FormatCheckResponse{
			Success: false,
			Message: "Nur YouTube URLs sind erlaubt",
		})
		return
	}

	// Clean URL
	cleanedURL, err := cleanURL(req.URL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FormatCheckResponse{
			Success: false,
			Message: "Ungültige URL",
		})
		return
	}

	// Run yt-dlp with format listing and JSON output for detailed info
	cmd := exec.Command("yt-dlp",
		"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"-F",
		"--no-warnings",
		cleanedURL)
	output, err := cmd.CombinedOutput()

	response := FormatCheckResponse{
		Success:  true,
		HasSABR:  false,
		Warnings: []string{},
	}

	outputStr := string(output)

	// Check for SABR warnings in output
	if strings.Contains(outputStr, "SABR") || strings.Contains(outputStr, "missing a url") {
		response.HasSABR = true
		response.Warnings = append(response.Warnings, "SABR-Streaming erkannt - einige Formate möglicherweise nicht verfügbar")
	}

	// Check for other warnings
	if strings.Contains(outputStr, "nsig extraction failed") {
		response.Warnings = append(response.Warnings, "Signatur-Extraktion fehlgeschlagen - einige Formate fehlen möglicherweise")
	}

	if err != nil {
		response.Success = false
		response.Message = "Fehler beim Abrufen der Formatinformationen"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	// Parse format output to get best quality info
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		// Look for best video format lines (usually contains resolution like 1080p, 720p)
		if strings.Contains(line, "mp4") && (strings.Contains(line, "1080p") || strings.Contains(line, "720p") || strings.Contains(line, "2160p")) {
			if response.BestVideoInfo == "" {
				response.BestVideoInfo = strings.TrimSpace(line)
			}
		}
		// Look for best audio format
		if strings.Contains(line, "audio only") && (strings.Contains(line, "m4a") || strings.Contains(line, "webm")) {
			if response.BestAudioInfo == "" {
				response.BestAudioInfo = strings.TrimSpace(line)
			}
		}
	}

	// Determine what will actually be downloaded based on format
	switch req.Format {
	case "mp4":
		response.SelectedFormat = "Bestes Video (MP4) + Audio zusammengeführt"
	case "mp3":
		response.SelectedFormat = "Beste Audio-Qualität → MP3 konvertiert"
	case "wav":
		response.SelectedFormat = "Beste Audio-Qualität → WAV konvertiert"
	case "m4a":
		response.SelectedFormat = "Beste Audio-Qualität → M4A konvertiert"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func sendJSONResponse(w http.ResponseWriter, response DownloadResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// reportBackendError sends backend errors to Slack automatically
func reportBackendError(errorMsg string, context map[string]string) {
	if slackWebhookURL == "" {
		return // Silently skip if not configured
	}

	go func() {
		report := ErrorReport{
			ErrorMessage: errorMsg,
			ErrorStack:   "",
			URL:          "Backend Error",
			UserAgent:    "Go Backend",
			Timestamp:    time.Now().Format(time.RFC3339),
			SessionID:    "backend-" + time.Now().Format("20060102-150405"),
			LastActions:  []string{},
			BrowserInfo:  context,
		}

		if err := sendSlackNotification(report); err != nil {
			log.Printf("[BackendError] Failed to send Slack notification: %v", err)
		}
	}()
}

// sendSlackNotification sends a formatted error report to Slack
func sendSlackNotification(report ErrorReport) error {
	if slackWebhookURL == "" {
		log.Printf("[Slack] Warning: SLACK_WEBHOOK_URL not configured, skipping notification")
		return nil
	}

	// Build Slack message with rich formatting
	message := SlackMessage{
		Text: "🚨 YouTube Downloader Error Report",
		Attachments: []SlackAttachment{
			{
				Color: "danger",
				Fields: []SlackField{
					{
						Title: "Error Message",
						Value: report.ErrorMessage,
						Short: false,
					},
					{
						Title: "URL",
						Value: report.URL,
						Short: true,
					},
					{
						Title: "Timestamp",
						Value: report.Timestamp,
						Short: true,
					},
					{
						Title: "User Agent",
						Value: report.UserAgent,
						Short: false,
					},
					{
						Title: "Session ID",
						Value: report.SessionID,
						Short: true,
					},
					{
						Title: "Browser",
						Value: fmt.Sprintf("%s %s on %s",
							report.BrowserInfo["name"],
							report.BrowserInfo["version"],
							report.BrowserInfo["os"]),
						Short: true,
					},
				},
			},
		},
	}

	// Add stack trace if available
	if report.ErrorStack != "" {
		message.Attachments[0].Fields = append(message.Attachments[0].Fields, SlackField{
			Title: "Stack Trace",
			Value: fmt.Sprintf("```%s```", truncateString(report.ErrorStack, 500)),
			Short: false,
		})
	}

	// Add last actions if available
	if len(report.LastActions) > 0 {
		actionsText := ""
		for i, action := range report.LastActions {
			actionsText += fmt.Sprintf("%d. %s\n", i+1, action)
		}
		message.Attachments[0].Fields = append(message.Attachments[0].Fields, SlackField{
			Title: "Last Actions",
			Value: actionsText,
			Short: false,
		})
	}

	// Send to Slack
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal Slack message: %v", err)
	}

	resp, err := http.Post(slackWebhookURL, "application/json", strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("failed to send Slack notification: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack returned status %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("[Slack] Error report sent successfully for session %s", report.SessionID)
	return nil
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleErrorReport handles error reports from the frontend
func handleErrorReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var report ErrorReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		log.Printf("[ErrorReport] Failed to decode error report: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Add server timestamp
	if report.Timestamp == "" {
		report.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Log error locally
	log.Printf("[ErrorReport] Error received from frontend:")
	log.Printf("[ErrorReport]   Message: %s", report.ErrorMessage)
	log.Printf("[ErrorReport]   URL: %s", report.URL)
	log.Printf("[ErrorReport]   User-Agent: %s", report.UserAgent)
	log.Printf("[ErrorReport]   Session: %s", report.SessionID)
	if len(report.LastActions) > 0 {
		log.Printf("[ErrorReport]   Last Actions: %v", report.LastActions)
	}
	if report.ErrorStack != "" {
		log.Printf("[ErrorReport]   Stack: %s", report.ErrorStack)
	}

	// Send to Slack
	go func() {
		if err := sendSlackNotification(report); err != nil {
			log.Printf("[ErrorReport] Failed to send Slack notification: %v", err)
		}
	}()

	// Respond to frontend
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// sendStartupNotification sends a notification to Slack when the service starts
func sendStartupNotification() {
	if slackWebhookURL == "" {
		log.Printf("[Startup] SLACK_WEBHOOK_URL not configured, skipping startup notification")
		return
	}

	// Get hostname
	hostname, _ := os.Hostname()

	// Get yt-dlp version
	ytdlpVersion := "unknown"
	cmd := exec.Command("yt-dlp", "--version")
	if output, err := cmd.Output(); err == nil {
		ytdlpVersion = strings.TrimSpace(string(output))
	}

	message := SlackMessage{
		Text: "✅ YouTube Downloader gestartet",
		Attachments: []SlackAttachment{
			{
				Color: "good",
				Fields: []SlackField{
					{
						Title: "Status",
						Value: "🚀 Service läuft wieder",
						Short: true,
					},
					{
						Title: "Hostname",
						Value: hostname,
						Short: true,
					},
					{
						Title: "Timestamp",
						Value: time.Now().Format("2006-01-02 15:04:05 MST"),
						Short: true,
					},
					{
						Title: "yt-dlp Version",
						Value: ytdlpVersion,
						Short: true,
					},
				},
			},
		},
	}

	payload, err := json.Marshal(message)
	if err != nil {
		log.Printf("[Startup] Failed to marshal Slack message: %v", err)
		return
	}

	resp, err := http.Post(slackWebhookURL, "application/json", strings.NewReader(string(payload)))
	if err != nil {
		log.Printf("[Startup] Failed to send Slack notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Startup] Slack returned status %d: %s", resp.StatusCode, string(body))
		return
	}

	log.Printf("[Startup] Startup notification sent to Slack")
}

// handleTestSlack is a test endpoint to verify Slack notifications work
func handleTestSlack(w http.ResponseWriter, r *http.Request) {
	if slackWebhookURL == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "SLACK_WEBHOOK_URL not configured",
		})
		return
	}

	// Create a test error report
	testReport := ErrorReport{
		ErrorMessage: "Test Error Report - Slack Integration Test",
		ErrorStack:   "at handleTestSlack (main.go:1250)\nat http.HandlerFunc.ServeHTTP (net/http/server.go:2136)",
		URL:          "https://music.hasenkamp.dev/test-slack",
		UserAgent:    r.Header.Get("User-Agent"),
		Timestamp:    time.Now().Format(time.RFC3339),
		SessionID:    "test-session-" + time.Now().Format("20060102-150405"),
		LastActions: []string{
			"[Test] User navigated to /test-slack",
			"[Test] Triggered manual Slack test",
			"[Test] Generating test error report",
		},
		BrowserInfo: map[string]string{
			"name":    "Test Browser",
			"version": "1.0.0",
			"os":      "Test OS",
		},
	}

	log.Printf("[TestSlack] Sending test notification to Slack...")

	// Send to Slack
	if err := sendSlackNotification(testReport); err != nil {
		log.Printf("[TestSlack] Failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Failed to send to Slack: %v", err),
		})
		return
	}

	log.Printf("[TestSlack] Test notification sent successfully!")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Test notification sent to Slack! Check your channel.",
	})
}

// cleanupCompletedDownloads runs periodically to remove old completed downloads from cache
func cleanupCompletedDownloads() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		progressMutex.Lock()
		now := time.Now()
		for sessionID, completed := range completedDownloads {
			if now.Sub(completed.CompletedAt) > completedCacheTTL {
				delete(completedDownloads, sessionID)
				log.Printf("[Cleanup] Removed old completed download: %s", sessionID)
			}
		}
		progressMutex.Unlock()
	}
}
