package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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

var (
	progressClients = make(map[string]chan ProgressUpdate)
	progressMutex   sync.RWMutex
)

func main() {
	// Serve static files
	http.Handle("/", http.FileServer(http.Dir("./static")))

	// Download endpoint
	http.HandleFunc("/download", handleDownload)
	http.HandleFunc("/progress", handleProgress)
	http.HandleFunc("/download-file/", handleDownloadFile)
	http.HandleFunc("/check-formats", handleCheckFormats)

	// Check if yt-dlp is installed
	if err := checkYtDlp(); err != nil {
		log.Printf("Warning: yt-dlp not found. Please install it: %v", err)
	}

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

// cleanURL entfernt Playlist-Parameter und andere unerwünschte URL-Teile
func cleanURL(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Expand youtu.be short URLs to full youtube.com URLs
	if parsedURL.Host == "youtu.be" {
		videoID := strings.TrimPrefix(parsedURL.Path, "/")
		// Reconstruct as full YouTube URL
		parsedURL.Host = "www.youtube.com"
		parsedURL.Path = "/watch"
		query := parsedURL.Query()
		query.Set("v", videoID)
		parsedURL.RawQuery = query.Encode()
	}

	// Entferne list, index und andere Playlist-bezogene Parameter
	query := parsedURL.Query()
	query.Del("list")
	query.Del("index")
	query.Del("start_radio")
	query.Del("si") // Remove YouTube tracking parameter
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String(), nil
}

func handleProgress(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	// Server-Sent Events Headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create channel for this client
	progressChan := make(chan ProgressUpdate, 10)

	progressMutex.Lock()
	progressClients[sessionID] = progressChan
	progressMutex.Unlock()

	// Clean up on disconnect
	defer func() {
		progressMutex.Lock()
		delete(progressClients, sessionID)
		close(progressChan)
		progressMutex.Unlock()
	}()

	// Send updates to client
	for update := range progressChan {
		data, _ := json.Marshal(update)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
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
			sendProgress(sessionID, 0, fmt.Sprintf("Error: %v", err))
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

	progressMutex.RLock()
	defer progressMutex.RUnlock()

	if ch, ok := progressClients[sessionID]; ok {
		select {
		case ch <- ProgressUpdate{Progress: progress, Status: status}:
		default:
			// Client disconnected or channel full
		}
	}
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

	switch format {
	case "mp4":
		args = []string{
			"-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
			"--merge-output-format", "mp4",
			"--no-playlist",
			"-o", outputTemplate,
			url,
		}
	case "mp3":
		args = []string{
			"-x",
			"--audio-format", "mp3",
			"--audio-quality", "0",
			"--no-playlist",
			"-o", outputTemplate,
			url,
		}
	case "wav":
		args = []string{
			"-x",
			"--audio-format", "wav",
			"--no-playlist",
			"-o", outputTemplate,
			url,
		}
	case "m4a":
		args = []string{
			"-x",
			"--audio-format", "m4a",
			"--audio-quality", "0",
			"--no-playlist",
			"-o", outputTemplate,
			url,
		}
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

	// Return just the filename (not the full path)
	return filepath.Base(files[0]), nil
}

func handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	// Extract filename from URL path
	filename := strings.TrimPrefix(r.URL.Path, "/download-file/")
	if filename == "" {
		log.Printf("Download file error: No filename provided")
		http.Error(w, "Dateiname fehlt", http.StatusBadRequest)
		return
	}

	// Security: Prevent directory traversal
	filename = filepath.Base(filename)

	// Build full path
	filePath := filepath.Join("./downloads", filename)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("Download file error: File not found: %s", filename)
		http.Error(w, "Datei nicht gefunden. Möglicherweise wurde sie bereits heruntergeladen.", http.StatusNotFound)
		return
	}

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
	cmd := exec.Command("yt-dlp", "-F", "--no-warnings", cleanedURL)
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
