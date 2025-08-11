package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type LogEntry struct {
	Timestamp        string `json:"timestamp"`
	Level            string `json:"level"`
	Message          string `json:"message"`
	JSONFile         string `json:"json_file"`
	JSONLine         int    `json:"json_line"`
	JSONClass        string `json:"json_class"`
	JSONFunction     string `json:"json_function"`
	JSONCode         int64  `json:"json_code"`
	ExceptionMessage string `json:"json_exceptionMessage"`
	Exception        string `json:"json_exception"`
	LogContext       string `json:"json_log_context"`
	PID              int    `json:"json_pid"`
	AppVersion       string `json:"json_app_version"`
	RequestURI       string `json:"json_request_uri"`
	CorrelationID    string `json:"json_correlation_id"`
	UserAgent        string `json:"json_user_agent"`
	RawLine          string `json:"raw_line"`
	JSONPart         string `json:"json_part"`
	IsRecent         bool   `json:"is_recent"`
	SourceFile       string `json:"source_file"`
}

var (
	infoEnabled      = flag.Bool("info", false, "Show INFO level logs")
	warnEnabled      = flag.Bool("warn", false, "Show WARN level logs")
	noticeEnabled    = flag.Bool("notice", false, "Show NOTICE level logs")
	debugEnabled     = flag.Bool("debug", false, "Show DEBUG level logs")
	errorEnabled     = flag.Bool("err", false, "Show ERROR/ERR level logs")
	critEnabled      = flag.Bool("crit", false, "Show CRIT/CRITICAL level logs")
	allLevels        = flag.Bool("all", false, "Show all log levels")
	highlightMinutes = flag.Int("minutes", 1, "Number of minutes to highlight recent logs (0 to disable)")
	logLines         = flag.Int("lines", 200, "Number of log lines to display")
	port             = flag.String("port", "1111", "Port to serve on")
	logPath          = ""
	isDirectory      = false
)

func shouldShowLogLevel(level string) bool {
	level = strings.TrimSpace(strings.ToUpper(level))

	if *allLevels || (!*infoEnabled && !*warnEnabled && !*noticeEnabled && !*debugEnabled && !*errorEnabled && !*critEnabled) {
		return true
	}

	switch level {
	case "INFO":
		return *infoEnabled
	case "WARN":
		return *warnEnabled
	case "NOTICE":
		return *noticeEnabled
	case "DEBUG":
		return *debugEnabled
	case "ERROR", "ERR":
		return *errorEnabled
	case "CRIT", "CRITICAL":
		return *critEnabled
	default:
		return false
	}
}

func parseTimestamp(timestamp string) (time.Time, error) {
	layouts := []string{
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
	}

	// First try parsing with local timezone
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, timestamp, time.Local); err == nil {
			return t, nil
		}
	}

	// If local parsing fails, try UTC layouts
	for _, layout := range layouts {
		if t, err := time.Parse(layout, timestamp); err == nil {
			// Only convert to local if it's explicitly in UTC
			if t.Location() == time.UTC {
				return t.In(time.Local), nil
			}
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("could not parse timestamp: %s", timestamp)
}

func parseLogLine(line string) (*LogEntry, error) {
	// Handle format: "2024-01-15 10:15:23 INFO Test message {...}"
	if strings.Contains(line, " - ") {
		// Handle old format: "[timestamp] - LEVEL message ~> {...}"
		return parseOldFormat(line)
	}

	// New format: "2024-01-15 10:15:23 INFO Test message {...}"
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return nil, nil
	}

	// Extract timestamp (first two parts)
	timestamp := parts[0] + " " + parts[1]

	// Extract level (third part)
	rawLevel := strings.TrimSpace(strings.ToUpper(parts[2]))
	if rawLevel == "ERR" {
		rawLevel = "ERROR"
	}
	if rawLevel == "CRITICAL" {
		rawLevel = "CRIT"
	}

	// Extract message and JSON part
	rest := parts[3]
	jsonStartIdx := strings.Index(rest, "{")

	var plainMessage string
	var jsonStr string

	if jsonStartIdx != -1 {
		plainMessage = strings.TrimSpace(rest[:jsonStartIdx])
		jsonStr = strings.TrimSpace(rest[jsonStartIdx:])
	} else {
		plainMessage = rest
		jsonStr = ""
	}

	// Parse timestamp for recency check
	parsedTime, err := parseTimestamp(timestamp)
	isRecent := false
	if err == nil {
		now := time.Now().In(parsedTime.Location())
		duration := now.Sub(parsedTime)
		if duration < 0 {
			duration = -duration
		}
		windowDuration := time.Duration(*highlightMinutes) * time.Minute
		isRecent = *highlightMinutes > 0 && duration <= windowDuration
	}

	entry := &LogEntry{
		Timestamp: timestamp,
		Level:     rawLevel,
		Message:   plainMessage,
		RawLine:   line,
		IsRecent:  isRecent,
	}

	// Parse JSON part if present
	if jsonStr != "" {
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, []byte(jsonStr), "", "  "); err == nil {
			entry.JSONPart = html.EscapeString(prettyJSON.String())
		} else {
			entry.JSONPart = html.EscapeString(jsonStr)
		}

		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &jsonData); err == nil {
			if val, ok := jsonData["json_file"].(string); ok {
				entry.JSONFile = val
			}
			if val, ok := jsonData["json_line"].(float64); ok {
				entry.JSONLine = int(val)
			}
			if val, ok := jsonData["json_class"].(string); ok {
				entry.JSONClass = val
			}
			if val, ok := jsonData["json_function"].(string); ok {
				entry.JSONFunction = val
			}
			if val, ok := jsonData["json_code"].(float64); ok {
				entry.JSONCode = int64(val)
			}
			if val, ok := jsonData["json_log_context"].(string); ok {
				entry.LogContext = val
			}
			if val, ok := jsonData["json_pid"].(float64); ok {
				entry.PID = int(val)
			}
			if val, ok := jsonData["json_app_version"].(string); ok {
				entry.AppVersion = val
			}
			if val, ok := jsonData["json_request_uri"].(string); ok {
				entry.RequestURI = val
			}
			if val, ok := jsonData["json_correlation_id"].(string); ok {
				entry.CorrelationID = val
			}
			if val, ok := jsonData["json_user_agent"].(string); ok {
				entry.UserAgent = val
			}
			if val, ok := jsonData["json_exceptionMessage"].(string); ok {
				entry.ExceptionMessage = val
			}
			if val, ok := jsonData["json_exception"].(string); ok {
				entry.Exception = val
			}
		}
	}

	return entry, nil
}

func parseOldFormat(line string) (*LogEntry, error) {
	parts := strings.Split(line, " - ")
	if len(parts) < 2 {
		return nil, nil
	}

	levelAndMsg := strings.SplitN(parts[1], "~>", 2)
	rawLevel := ""
	plainMessage := ""

	if bracketIdx := strings.Index(levelAndMsg[0], "("); bracketIdx != -1 {
		rawLevel = strings.TrimSpace(levelAndMsg[0][:bracketIdx])
		plainMessage = strings.TrimSpace(levelAndMsg[0][strings.Index(levelAndMsg[0], "]:")+2:])
	} else {
		levelParts := strings.Fields(levelAndMsg[0])
		if len(levelParts) > 0 {
			rawLevel = levelParts[0]
			if len(levelParts) > 1 {
				plainMessage = strings.Join(levelParts[1:], " ")
			}
		}
	}

	// Normalize error levels
	rawLevel = strings.TrimSpace(strings.ToUpper(rawLevel))
	if rawLevel == "ERR" {
		rawLevel = "ERROR"
	}
	if rawLevel == "CRITICAL" {
		rawLevel = "CRIT"
	}

	timestamp := strings.TrimSpace(strings.TrimLeft(parts[0], "["))
	parsedTime, err := parseTimestamp(timestamp)
	isRecent := false
	if err == nil {
		now := time.Now().In(parsedTime.Location())
		duration := now.Sub(parsedTime)
		if duration < 0 {
			duration = -duration
		}
		windowDuration := time.Duration(*highlightMinutes) * time.Minute
		isRecent = *highlightMinutes > 0 && duration <= windowDuration
	}

	entry := &LogEntry{
		Timestamp: timestamp,
		Level:     rawLevel,
		Message:   plainMessage,
		RawLine:   line,
		IsRecent:  isRecent,
	}

	if len(levelAndMsg) > 1 {
		jsonStr := strings.TrimSpace(levelAndMsg[1])

		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, []byte(jsonStr), "", "  "); err == nil {
			entry.JSONPart = html.EscapeString(prettyJSON.String())
		} else {
			entry.JSONPart = html.EscapeString(jsonStr)
		}

		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &jsonData); err == nil {
			if val, ok := jsonData["json_file"].(string); ok {
				entry.JSONFile = val
			}
			if val, ok := jsonData["json_line"].(float64); ok {
				entry.JSONLine = int(val)
			}
			if val, ok := jsonData["json_class"].(string); ok {
				entry.JSONClass = val
			}
			if val, ok := jsonData["json_function"].(string); ok {
				entry.JSONFunction = val
			}
			if val, ok := jsonData["json_code"].(float64); ok {
				entry.JSONCode = int64(val)
			}
			if val, ok := jsonData["json_log_context"].(string); ok {
				entry.LogContext = val
			}
			if val, ok := jsonData["json_pid"].(float64); ok {
				entry.PID = int(val)
			}
			if val, ok := jsonData["json_app_version"].(string); ok {
				entry.AppVersion = val
			}
			if val, ok := jsonData["json_request_uri"].(string); ok {
				entry.RequestURI = val
			}
			if val, ok := jsonData["json_correlation_id"].(string); ok {
				entry.CorrelationID = val
			}
			if val, ok := jsonData["json_user_agent"].(string); ok {
				entry.UserAgent = val
			}
			if val, ok := jsonData["json_exceptionMessage"].(string); ok {
				entry.ExceptionMessage = val
			}
			if val, ok := jsonData["json_exception"].(string); ok {
				entry.Exception = val
			}
		}
	}

	return entry, nil
}

func readLastNLines(filePath string, n int) ([]LogEntry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(content), "\n")

	var entries []LogEntry
	for i := len(lines) - 1; i >= 0 && len(entries) < n; i-- {
		if lines[i] == "" {
			continue
		}

		entry, err := parseLogLine(lines[i])
		if err != nil {
			log.Printf("Error parsing line: %v", err)
			continue
		}
		if entry != nil && shouldShowLogLevel(entry.Level) {
			entries = append([]LogEntry{*entry}, entries...)
		}
	}

	// Sort entries by timestamp (newest first)
	sort.Slice(entries, func(i, j int) bool {
		time1, err1 := parseTimestamp(entries[i].Timestamp)
		time2, err2 := parseTimestamp(entries[j].Timestamp)

		if err1 != nil || err2 != nil {
			return false
		}

		return time1.After(time2)
	})

	return entries, nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <path-to-log-file>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -all      Show all log levels\n")
		fmt.Fprintf(os.Stderr, "  -crit     Show CRIT/CRITICAL level logs\n")
		fmt.Fprintf(os.Stderr, "  -err      Show ERROR/ERR level logs\n")
		fmt.Fprintf(os.Stderr, "  -warn     Show WARN level logs\n")
		fmt.Fprintf(os.Stderr, "  -info     Show INFO level logs\n")
		fmt.Fprintf(os.Stderr, "  -notice   Show NOTICE level logs\n")
		fmt.Fprintf(os.Stderr, "  -debug    Show DEBUG level logs\n")
		fmt.Fprintf(os.Stderr, "  -minutes  Number of minutes to highlight recent logs (default: 1, 0 to disable)\n")
		fmt.Fprintf(os.Stderr, "  -lines    Number of log lines to display (default: 200)\n")
		fmt.Fprintf(os.Stderr, "  -port     Port to serve on (default: 1111)\n\n")
		fmt.Fprintf(os.Stderr, "If no log levels are specified, all levels will be shown.\n")
		fmt.Fprintf(os.Stderr, "You can combine multiple flags to show multiple levels.\n")
	}

	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	logPath = flag.Arg(0)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		log.Fatalf("Log file or directory does not exist: %s", logPath)
	}

	isDirectory = false
	if stat, err := os.Stat(logPath); err == nil && stat.IsDir() {
		isDirectory = true
		log.Printf("Reading directory: %s", logPath)
	} else {
		log.Printf("Reading file: %s", logPath)
	}

	var activeFilters []string
	if *critEnabled {
		activeFilters = append(activeFilters, "CRIT")
	}
	if *errorEnabled {
		activeFilters = append(activeFilters, "ERROR")
	}
	if *warnEnabled {
		activeFilters = append(activeFilters, "WARN")
	}
	if *infoEnabled {
		activeFilters = append(activeFilters, "INFO")
	}
	if *noticeEnabled {
		activeFilters = append(activeFilters, "NOTICE")
	}
	if *debugEnabled {
		activeFilters = append(activeFilters, "DEBUG")
	}
	if *allLevels || len(activeFilters) == 0 {
		log.Printf("Showing all log levels")
	} else {
		log.Printf("Filtering log levels: %s", strings.Join(activeFilters, ", "))
	}

	if *highlightMinutes > 0 {
		log.Printf("Highlighting logs from the last %d minute(s)", *highlightMinutes)
	} else {
		log.Printf("Log highlighting is disabled")
	}

	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal("Could not get static files:", err)
	}
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Removed debug print statement

		tmplContent, err := templateFS.ReadFile("templates/index.html")
		if err != nil {
			log.Printf("Error reading template: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		tmpl, err := template.New("index.html").Parse(string(tmplContent))
		if err != nil {
			log.Printf("Error parsing template: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		tmpl.Execute(w, nil)
	})

	http.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		var entries []LogEntry
		var err error

		// Get search parameters
		searchText := r.URL.Query().Get("search")
		linesCount := *logLines
		if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
			if parsed, err := strconv.Atoi(linesParam); err == nil && parsed > 0 {
				linesCount = parsed
			}
		}

		if isDirectory {
			entries, err = readDirectoryLogs(logPath, linesCount)
		} else {
			entries, err = readLastNLines(logPath, linesCount)
		}

		if err != nil {
			log.Printf("Error reading log file/directory: %v", err)
			http.Error(w, "Error reading log file/directory", http.StatusInternalServerError)
			return
		}

		// Apply search filter if provided
		if searchText != "" {
			entries = filterEntriesBySearch(entries, searchText)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})

	// Info handler
	http.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		info := map[string]interface{}{
			"path":         logPath,
			"is_directory": isDirectory,
		}

		if isDirectory {
			// Count files in directory
			fileCount := 0
			filepath.Walk(logPath, func(path string, fileInfo os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if !fileInfo.IsDir() && (strings.HasSuffix(path, ".log") || strings.HasSuffix(path, ".txt")) {
					fileCount++
				}
				return nil
			})
			info["file_count"] = fileCount
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})

	log.Printf("Starting log viewer for %s on http://localhost:%s", logPath, *port)
	if err := http.ListenAndServe(":"+*port, nil); err != nil {
		log.Fatal(err)
	}
}

func readDirectoryLogs(dirPath string, n int) ([]LogEntry, error) {
	var allEntries []LogEntry

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Skip non-log files (optional: could make this configurable)
		if !strings.HasSuffix(path, ".log") && !strings.HasSuffix(path, ".txt") {
			return nil
		}

		fileEntries, err := readLastNLines(path, n)
		if err != nil {
			log.Printf("Error reading file %s: %v", path, err)
			return nil // Continue walking other files
		}

		// Add source file information to each entry
		for i := range fileEntries {
			fileEntries[i].SourceFile = filepath.Base(path)
		}

		allEntries = append(allEntries, fileEntries...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort entries by timestamp (newest first)
	sort.Slice(allEntries, func(i, j int) bool {
		time1, err1 := parseTimestamp(allEntries[i].Timestamp)
		time2, err2 := parseTimestamp(allEntries[j].Timestamp)

		if err1 != nil || err2 != nil {
			return false
		}

		return time1.After(time2)
	})

	// Return only the most recent N entries
	if len(allEntries) > n {
		allEntries = allEntries[:n]
	}

	return allEntries, nil
}

func filterEntriesBySearch(entries []LogEntry, searchText string) []LogEntry {
	if searchText == "" {
		return entries
	}

	searchText = strings.ToLower(searchText)
	var filteredEntries []LogEntry

	for _, entry := range entries {
		// Search in message, JSON fields, and code
		if strings.Contains(strings.ToLower(entry.Message), searchText) ||
			strings.Contains(strings.ToLower(entry.JSONPart), searchText) ||
			strings.Contains(strings.ToLower(entry.LogContext), searchText) ||
			strings.Contains(strings.ToLower(entry.JSONFunction), searchText) ||
			strings.Contains(strings.ToLower(entry.JSONClass), searchText) ||
			strings.Contains(strings.ToLower(entry.JSONFile), searchText) ||
			strings.Contains(strings.ToLower(entry.RequestURI), searchText) ||
			strings.Contains(strings.ToLower(entry.CorrelationID), searchText) ||
			strings.Contains(strings.ToLower(entry.Exception), searchText) ||
			strings.Contains(strings.ToLower(entry.ExceptionMessage), searchText) ||
			strings.Contains(strings.ToLower(strconv.FormatInt(entry.JSONCode, 10)), searchText) {
			filteredEntries = append(filteredEntries, entry)
		}
	}

	return filteredEntries
}
