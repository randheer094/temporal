package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"temporal/docs"
	"temporal/internal/filewriter"
	"temporal/internal/rules"
	"time"

	"github.com/tidwall/gjson"

	httpSwagger "github.com/swaggo/http-swagger"
)

const (
	eventsLogMaxBytes int64 = 10 * 1024 * 1024 // 10MB; rotates to events.log.1
	defaultPageSize         = 50
	maxPageSize             = 500
)

// Event is the legacy POST /events payload (unchanged for backwards compat).
type Event struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

type API struct {
	logDir    string
	events    *filewriter.Writer
	daemon    *filewriter.Writer
	rulesPath string
}

func NewAPI(logDir string) (*API, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("could not create log directory: %w", err)
	}
	events, err := filewriter.NewWithMaxSize(filepath.Join(logDir, "events.log"), eventsLogMaxBytes)
	if err != nil {
		return nil, err
	}
	daemon, err := filewriter.New(filepath.Join(logDir, "daemon.log"))
	if err != nil {
		events.Close()
		return nil, err
	}
	return &API{
		logDir:    logDir,
		events:    events,
		daemon:    daemon,
		rulesPath: filepath.Join(logDir, "rules.yaml"),
	}, nil
}

func (a *API) Close() {
	a.events.Close()
	a.daemon.Close()
}

// @title Temporal API
// @version 1.0
// @description API for the Temporal event logger
// @BasePath /
//
// POST /events accepts arbitrary JSON (object or array). The body is matched
// against rules.yaml; matching rules render templates via gjson and may
// fan out across nested arrays via `each`. Bodies with no rule match fall
// back to the legacy {type,title,message} Event shape. Writes are queued
// by filewriter; the handler always returns 200 and only persists entries
// when the rule produced non-empty output.
func (a *API) logEventHandler(w http.ResponseWriter, r *http.Request) {
	a.logDaemon("Received request on " + r.URL.Path)
	if r.Method != http.MethodPost {
		// Even non-POST returns 200 to honor the "always 200" contract; we
		// just skip processing.
		respondOK(w, "ignored")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.logDaemon("read body failed: " + err.Error())
		respondOK(w, "ignored")
		return
	}
	defer r.Body.Close()

	rs, rulesErr := rules.Load(a.rulesPath)
	if rulesErr != nil {
		a.logDaemon("rules load failed: " + rulesErr.Error())
	}

	written := 0
	if isJSONArray(body) {
		gjson.ParseBytes(body).ForEach(func(_, item gjson.Result) bool {
			written += a.processItem(rs, []byte(item.Raw))
			return true
		})
	} else {
		written = a.processItem(rs, body)
	}

	if written == 0 {
		respondOK(w, "no_match")
		return
	}
	respondOK(w, "ok")
}

// processItem applies any matching rule to the body, falling back to the
// legacy Event shape when no rule matches. Returns the number of log
// entries queued (rules with `each` may produce multiple).
func (a *API) processItem(rs *rules.RuleSet, body []byte) int {
	if rs != nil && len(rs.Rules) > 0 {
		target := rules.ExtractTarget(body)
		if rule := rs.Find(target); rule != nil {
			results := rule.Apply(body)
			for _, res := range results {
				a.writeEvent(res.Type, res.Title, res.Message)
			}
			return len(results)
		}
	}
	var entry Event
	if err := json.Unmarshal(body, &entry); err != nil {
		return 0
	}
	if entry.Type == "" && entry.Title == "" && entry.Message == "" {
		return 0
	}
	msgs := []string(nil)
	if entry.Message != "" {
		msgs = []string{entry.Message}
	}
	a.writeEvent(entry.Type, entry.Title, msgs)
	return 1
}

func isJSONArray(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

// writeEvent serializes one log entry. Each message in `messages` is written
// on its own line between TITLE and the END marker. Newlines inside any
// field are flattened to spaces so the line-based parser stays simple.
func (a *API) writeEvent(eventType, title string, messages []string) {
	timestamp := time.Now().Format(time.RFC3339)
	var buf strings.Builder
	buf.WriteString("*****START*****\n")
	buf.WriteString(timestamp)
	buf.WriteByte(' ')
	buf.WriteString(sanitizeLine(eventType))
	buf.WriteByte('\n')
	buf.WriteString(sanitizeLine(title))
	buf.WriteByte('\n')
	for _, m := range messages {
		buf.WriteString(sanitizeLine(m))
		buf.WriteByte('\n')
	}
	buf.WriteString("*****END*****\n")
	a.events.Write(buf.String())
}

func sanitizeLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

func respondOK(w http.ResponseWriter, status string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

// @Summary Log an event
// @Description Log an event
// @ID log-event
// @Accept  json
// @Produce  json
// @Param   entry     body    Event     true        "Log Entry"
// @Success 200 {object} map[string]string
// @Router /events [post]
func (a *API) Run() {
	docs.SwaggerInfo.BasePath = "/"
	mux := a.routes()
	a.logDaemon("Server starting on port 8005...")
	if err := http.ListenAndServe(":8005", mux); err != nil {
		a.logDaemon(fmt.Sprintf("Server failed to start: %v", err))
		log.Fatal("Server failed to start:", err)
	}
}

func (a *API) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/events", a.logEventHandler)
	mux.HandleFunc("/logs", a.logsPageHandler)
	mux.HandleFunc("/logs.json", a.logsJSONHandler)
	mux.HandleFunc("/api/docs/", httpSwagger.Handler(
		httpSwagger.URL("http://localhost:8005/swagger/doc.json"),
	))
	return mux
}

func (a *API) logDaemon(message string) {
	a.daemon.Write(fmt.Sprintf("%s: %s\n", time.Now().Format(time.RFC3339), message))
}

// --- Logs viewer ---

type logEntry struct {
	Timestamp string   `json:"timestamp"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Message   []string `json:"message"`
}

type logsResponse struct {
	Entries    []logEntry `json:"entries"`
	Page       int        `json:"page"`
	Size       int        `json:"size"`
	Total      int        `json:"total"`
	TotalPages int        `json:"total_pages"`
	Types      []string   `json:"types"`
}

func (a *API) logsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/logs" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(logsHTML)
}

func (a *API) logsJSONHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	typeFilter := r.URL.Query().Get("type")

	entries := a.readAllEvents()
	// Newest first.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp > entries[j].Timestamp
	})

	types := uniqueTypes(entries)

	if typeFilter != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Type == typeFilter {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	total := len(entries)
	totalPages := (total + size - 1) / size
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}

	resp := logsResponse{
		Entries:    entries[start:end],
		Page:       page,
		Size:       size,
		Total:      total,
		TotalPages: totalPages,
		Types:      types,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// readAllEvents merges the active log and the rotated backup so the UI can
// page across the full retained window.
func (a *API) readAllEvents() []logEntry {
	var out []logEntry
	// Read backup first (older entries) then current.
	for _, name := range []string{"events.log.1", "events.log"} {
		data, err := os.ReadFile(filepath.Join(a.logDir, name))
		if err != nil {
			continue
		}
		out = append(out, parseEventsLog(data)...)
	}
	return out
}

func parseEventsLog(data []byte) []logEntry {
	const (
		startMarker = "*****START*****"
		endMarker   = "*****END*****"
	)
	var entries []logEntry
	text := string(data)
	for {
		s := strings.Index(text, startMarker)
		if s < 0 {
			break
		}
		text = text[s+len(startMarker):]
		e := strings.Index(text, endMarker)
		if e < 0 {
			break
		}
		block := strings.TrimSpace(text[:e])
		text = text[e+len(endMarker):]

		lines := strings.Split(block, "\n")
		if len(lines) < 1 {
			continue
		}
		header := strings.SplitN(lines[0], " ", 2)
		entry := logEntry{Timestamp: header[0]}
		if len(header) > 1 {
			entry.Type = header[1]
		}
		if len(lines) >= 2 {
			entry.Title = lines[1]
		}
		if len(lines) >= 3 {
			for _, m := range lines[2:] {
				if m == "" {
					continue
				}
				entry.Message = append(entry.Message, m)
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func uniqueTypes(entries []logEntry) []string {
	seen := map[string]struct{}{}
	for _, e := range entries {
		if e.Type == "" {
			continue
		}
		seen[e.Type] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
