package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

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
}

var (
	infoEnabled   = flag.Bool("info", false, "Show INFO level logs")
	warnEnabled   = flag.Bool("warn", false, "Show WARN level logs")
	noticeEnabled = flag.Bool("notice", false, "Show NOTICE level logs")
	debugEnabled  = flag.Bool("debug", false, "Show DEBUG level logs")
	errorEnabled  = flag.Bool("err", false, "Show ERROR/ERR level logs")
	allLevels     = flag.Bool("all", false, "Show all log levels")
)

func shouldShowLogLevel(level string) bool {
	level = strings.TrimSpace(strings.ToUpper(level))

	if *allLevels || (!*infoEnabled && !*warnEnabled && !*noticeEnabled && !*debugEnabled && !*errorEnabled) {
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
	default:
		return false
	}
}

func parseLogLine(line string) (*LogEntry, error) {
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

	entry := &LogEntry{
		Timestamp: strings.TrimSpace(parts[0]),
		Level:     rawLevel,
		Message:   plainMessage,
		RawLine:   line,
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

	return entries, nil
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <path-to-log-file>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -all      Show all log levels\n")
		fmt.Fprintf(os.Stderr, "  -info     Show INFO level logs\n")
		fmt.Fprintf(os.Stderr, "  -warn     Show WARN level logs\n")
		fmt.Fprintf(os.Stderr, "  -notice   Show NOTICE level logs\n")
		fmt.Fprintf(os.Stderr, "  -debug    Show DEBUG level logs\n")
		fmt.Fprintf(os.Stderr, "  -err      Show ERROR/ERR level logs\n\n")
		fmt.Fprintf(os.Stderr, "If no log levels are specified, all levels will be shown.\n")
		fmt.Fprintf(os.Stderr, "You can combine multiple flags to show multiple levels.\n")
	}

	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	logFile := flag.Arg(0)
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		log.Fatalf("Log file does not exist: %s", logFile)
	}

	var activeFilters []string
	if *infoEnabled {
		activeFilters = append(activeFilters, "INFO")
	}
	if *warnEnabled {
		activeFilters = append(activeFilters, "WARN")
	}
	if *noticeEnabled {
		activeFilters = append(activeFilters, "NOTICE")
	}
	if *debugEnabled {
		activeFilters = append(activeFilters, "DEBUG")
	}
	if *errorEnabled {
		activeFilters = append(activeFilters, "ERROR")
	}
	if *allLevels || len(activeFilters) == 0 {
		log.Printf("Showing all log levels")
	} else {
		log.Printf("Filtering log levels: %s", strings.Join(activeFilters, ", "))
	}

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl := template.Must(template.ParseFiles("templates/index.html"))
		tmpl.Execute(w, nil)
	})

	http.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		entries, err := readLastNLines(logFile, 100)
		if err != nil {
			log.Printf("Error reading log file: %v", err)
			http.Error(w, "Error reading log file", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	})

	log.Printf("Starting log viewer for %s on http://localhost:1111", logFile)
	if err := http.ListenAndServe(":1111", nil); err != nil {
		log.Fatal(err)
	}
}
