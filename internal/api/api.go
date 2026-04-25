package api

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"temporal/docs"
	"temporal/internal/filewriter"
	"time"

	httpSwagger "github.com/swaggo/http-swagger"
)

// Event represents the structure of the log data
type Event struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

type API struct {
	logDir string
	events *filewriter.Writer
	daemon *filewriter.Writer
}

func NewAPI(logDir string) (*API, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("could not create log directory: %w", err)
	}
	events, err := filewriter.New(filepath.Join(logDir, "events.log"))
	if err != nil {
		return nil, err
	}
	daemon, err := filewriter.New(filepath.Join(logDir, "daemon.log"))
	if err != nil {
		events.Close()
		return nil, err
	}
	return &API{logDir: logDir, events: events, daemon: daemon}, nil
}

func (a *API) Close() {
	a.events.Close()
	a.daemon.Close()
}

// @title Temporal API
// @version 1.0
// @description API for the Temporal event logger
// @BasePath /
func (a *API) logEventHandler(w http.ResponseWriter, r *http.Request) {
	a.logDaemon("Received request on /events")
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var entry Event
	if err := json.Unmarshal(body, &entry); err != nil {
		http.Error(w, "Error decoding JSON", http.StatusBadRequest)
		return
	}

	timestamp := time.Now().Format(time.RFC3339)
	logString := fmt.Sprintf("*****START*****\n%s %s\n%s\n%s\n*****END*****\n", timestamp, entry.Type, entry.Title, entry.Message)
	a.events.Write(logString)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
	http.HandleFunc("/events", a.logEventHandler)
	http.HandleFunc("/api/docs/", httpSwagger.Handler(
		httpSwagger.URL("http://localhost:8005/swagger/doc.json"), //The url pointing to API definition
	))

	a.logDaemon("Server starting on port 8005...")
	if err := http.ListenAndServe(":8005", nil); err != nil {
		a.logDaemon(fmt.Sprintf("Server failed to start: %v", err))
		log.Fatal("Server failed to start:", err)
	}
}

func (a *API) logDaemon(message string) {
	a.daemon.Write(fmt.Sprintf("%s: %s\n", time.Now().Format(time.RFC3339), message))
}
