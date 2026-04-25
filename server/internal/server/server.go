package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"temporal/docs"
	"time"

	httpSwagger "github.com/swaggo/http-swagger"
)

// Event represents the structure of the log data
type Event struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

// @title Temporal API
// @version 1.0
// @description API for the Temporal event logger
// @BasePath /
func (s *Server) logEventHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.logDaemon("Received request on /events"); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	logFile := filepath.Join(s.logDir, "events.log")

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

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		http.Error(w, "Error opening log file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format(time.RFC3339)

	logString := fmt.Sprintf("*****START*****\n%s %s\n%s\n%s\n*****END*****\n", timestamp, entry.Type, entry.Title, entry.Message)

	if _, err := f.WriteString(logString); err != nil {
		http.Error(w, "Error writing to log file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type Server struct {
	logDir string
}

func NewServer(logDir string) *Server {
	return &Server{logDir: logDir}
}

// @Summary Log an event
// @Description Log an event
// @ID log-event
// @Accept  json
// @Produce  json
// @Param   entry     body    Event     true        "Log Entry"
// @Success 200 {object} map[string]string
// @Router /events [post]
func (s *Server) Run() {
	docs.SwaggerInfo.BasePath = "/"
	http.HandleFunc("/events", s.logEventHandler)
	http.HandleFunc("/api/docs/", httpSwagger.Handler(
		httpSwagger.URL("http://localhost:8005/swagger/doc.json"), //The url pointing to API definition
	))

	if err := s.logDaemon("Server starting on port 8005..."); err != nil {
		log.Fatal("Failed to start logging:", err)
	}
	if err := http.ListenAndServe(":8005", nil); err != nil {
		if err := s.logDaemon(fmt.Sprintf("Server failed to start: %v", err)); err != nil {
			log.Fatal("Failed to start logging:", err)
		}
		log.Fatal("Server failed to start:", err)
	}
}

func (s *Server) logDaemon(message string) error {
	logFile := filepath.Join(s.logDir, "daemon.log")

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("could not open daemon log file: %w", err)
	}
	defer f.Close()

	daemonLogger := log.New(f, "", 0)
	daemonLogger.Printf("%s: %s\n", time.Now().Format(time.RFC3339), message)
	return nil
}
