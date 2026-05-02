package api

import (
	"crypto/rand"
	"encoding/hex"
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
	"sync"
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
	eventsLogName           = "events.log"
	eventsLogBackup         = "events.log.1"
)

// LogEntry is the on-disk and on-the-wire representation of a single event.
// events.log is a JSON-lines file: one LogEntry per line.
type LogEntry struct {
	ID        string   `json:"id"`
	Timestamp string   `json:"timestamp"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Message   []string `json:"message"`
}

type API struct {
	logDir    string
	events    *filewriter.Writer
	daemon    *filewriter.Writer
	rulesPath string

	// writeMu serializes writes against deletions. Writes hold it for the
	// brief queue-send; delete handlers hold it across the rewrite.
	writeMu sync.Mutex
}

func NewAPI(logDir string) (*API, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("could not create log directory: %w", err)
	}
	events, err := filewriter.NewWithMaxSize(filepath.Join(logDir, eventsLogName), eventsLogMaxBytes)
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
// fan out across nested arrays via `each`. Bodies with no rule match are
// dropped. Writes are queued by filewriter; the handler always returns 200
// and only persists entries when a rule produced non-empty output.
func (a *API) logEventHandler(w http.ResponseWriter, r *http.Request) {
	a.logDaemon("Received request on " + r.URL.Path)
	if r.Method != http.MethodPost {
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

// processItem applies the first matching rule to the body. Returns the
// number of log entries queued (rules with `each` may produce multiple);
// returns 0 when no rule matches or rules render empty.
func (a *API) processItem(rs *rules.RuleSet, body []byte) int {
	if rs == nil || len(rs.Rules) == 0 {
		return 0
	}
	rule := rs.Find(rules.ExtractTarget(body))
	if rule == nil {
		return 0
	}
	results := rule.Apply(body)
	for _, res := range results {
		a.writeEvent(res.Type, res.Title, res.Message)
	}
	return len(results)
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

// writeEvent serializes one log entry as a single JSON line.
func (a *API) writeEvent(eventType, title string, messages []string) {
	entry := LogEntry{
		ID:        newID(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Type:      eventType,
		Title:     title,
		Message:   messages,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		a.logDaemon("marshal event failed: " + err.Error())
		return
	}
	a.writeMu.Lock()
	a.events.Write(string(line) + "\n")
	a.writeMu.Unlock()
}

func newID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback: nanosecond timestamp — extremely unlikely to be reached.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

func respondOK(w http.ResponseWriter, status string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

// @Summary Log an event
// @Description Accepts arbitrary JSON (object or array); rules.yaml extracts log entries.
// @ID log-event
// @Accept  json
// @Produce  json
// @Param   body     body    object     true        "Arbitrary JSON payload"
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
	mux.HandleFunc("GET /logs", a.logsPageHandler)
	mux.HandleFunc("GET /logs.json", a.logsJSONHandler)
	mux.HandleFunc("DELETE /logs", a.deleteLogsHandler)
	mux.HandleFunc("DELETE /logs/{id}", a.deleteLogByIDHandler)
	mux.HandleFunc("/api/docs/", httpSwagger.Handler(
		httpSwagger.URL("http://localhost:8005/swagger/doc.json"),
	))
	return mux
}

func (a *API) logDaemon(message string) {
	a.daemon.Write(fmt.Sprintf("%s: %s\n", time.Now().Format(time.RFC3339), message))
}

// --- Logs viewer ---

type logsResponse struct {
	Entries    []LogEntry `json:"entries"`
	Page       int        `json:"page"`
	Size       int        `json:"size"`
	Total      int        `json:"total"`
	TotalPages int        `json:"total_pages"`
	Types      []string   `json:"types"`
}

func (a *API) logsPageHandler(w http.ResponseWriter, r *http.Request) {
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
	query := r.URL.Query().Get("q")

	entries := a.readAllEvents()
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp > entries[j].Timestamp
	})

	types := uniqueTypes(entries)
	entries = applyFilters(entries, typeFilter, query)

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
func (a *API) readAllEvents() []LogEntry {
	var out []LogEntry
	for _, name := range []string{eventsLogBackup, eventsLogName} {
		data, err := os.ReadFile(filepath.Join(a.logDir, name))
		if err != nil {
			continue
		}
		out = append(out, parseEventsLog(data)...)
	}
	return out
}

// parseEventsLog reads JSON-lines events. Malformed or non-JSON lines (e.g.
// blank lines, partial writes) are silently skipped.
func parseEventsLog(data []byte) []LogEntry {
	var entries []LogEntry
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		line := data[start:i]
		start = i + 1
		entry, ok := parseLine(line)
		if ok {
			entries = append(entries, entry)
		}
	}
	if start < len(data) {
		if entry, ok := parseLine(data[start:]); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func parseLine(line []byte) (LogEntry, bool) {
	for _, b := range line {
		if b == ' ' || b == '\t' || b == '\r' {
			continue
		}
		if b != '{' {
			return LogEntry{}, false
		}
		break
	}
	var e LogEntry
	if err := json.Unmarshal(line, &e); err != nil {
		return LogEntry{}, false
	}
	return e, true
}

func uniqueTypes(entries []LogEntry) []string {
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

// --- Delete handlers ---

// deleteLogsHandler removes entries matching the same filters /logs.json
// uses (?type= and ?q=). With no filters, deletes everything. With either
// filter set, only removes entries that match.
func (a *API) deleteLogsHandler(w http.ResponseWriter, r *http.Request) {
	typeFilter := r.URL.Query().Get("type")
	query := r.URL.Query().Get("q")
	keep := func(e LogEntry) bool {
		// Keep entries that DON'T match the filters.
		return !entryMatches(e, typeFilter, query)
	}
	removed, err := a.rewriteEvents(keep)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.logDaemon(fmt.Sprintf("deleted %d entries (type=%q q=%q)", removed, typeFilter, query))
	respondJSON(w, map[string]any{"status": "ok", "removed": removed})
}

// applyFilters returns entries whose Type matches typeFilter (when set)
// AND whose Title contains query (case-insensitive, when set).
func applyFilters(entries []LogEntry, typeFilter, query string) []LogEntry {
	if typeFilter == "" && query == "" {
		return entries
	}
	out := entries[:0]
	for _, e := range entries {
		if entryMatches(e, typeFilter, query) {
			out = append(out, e)
		}
	}
	return out
}

func entryMatches(e LogEntry, typeFilter, query string) bool {
	if typeFilter != "" && e.Type != typeFilter {
		return false
	}
	if query != "" && !strings.Contains(strings.ToLower(e.Title), strings.ToLower(query)) {
		return false
	}
	return true
}

// deleteLogByIDHandler removes a single entry by ID.
func (a *API) deleteLogByIDHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	keep := func(e LogEntry) bool { return e.ID != id }
	removed, err := a.rewriteEvents(keep)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if removed == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a.logDaemon(fmt.Sprintf("deleted entry id=%s", id))
	respondJSON(w, map[string]any{"status": "ok", "removed": removed})
}

func respondJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// rewriteEvents pauses the writer, rewrites both the active log and the
// rotated backup keeping only entries for which keep() returns true, then
// restarts the writer. Returns the number of entries removed.
func (a *API) rewriteEvents(keep func(LogEntry) bool) (int, error) {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()

	// Drain pending writes, then close so we can safely rewrite the file.
	a.events.Close()

	removed := 0
	for _, name := range []string{eventsLogBackup, eventsLogName} {
		path := filepath.Join(a.logDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			a.reopenWriter()
			return 0, fmt.Errorf("read %s: %w", name, err)
		}
		var buf []byte
		for _, e := range parseEventsLog(data) {
			if keep(e) {
				line, err := json.Marshal(e)
				if err != nil {
					continue
				}
				buf = append(buf, line...)
				buf = append(buf, '\n')
			} else {
				removed++
			}
		}
		if err := writeFileAtomic(path, buf); err != nil {
			a.reopenWriter()
			return removed, fmt.Errorf("write %s: %w", name, err)
		}
	}

	if err := a.reopenWriter(); err != nil {
		return removed, err
	}
	return removed, nil
}

func (a *API) reopenWriter() error {
	w, err := filewriter.NewWithMaxSize(filepath.Join(a.logDir, eventsLogName), eventsLogMaxBytes)
	if err != nil {
		a.logDaemon("reopen events writer failed: " + err.Error())
		return err
	}
	a.events = w
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
