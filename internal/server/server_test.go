package server

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogEventHandler(t *testing.T) {
	testDir := "test_output"
	// Clean up previous test runs
	os.RemoveAll(testDir)
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatal("Failed to create test directory:", err)
	}

	s := NewServer(testDir)

	// Create a test log entry
	entry := Event{
		Type:    "test-type",
		Title:   "test-title",
		Message: "test message",
	}
	body, _ := json.Marshal(entry)

	// Create a request and response recorder
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	// Call the handler
	s.logEventHandler(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			status, http.StatusOK)
	}

	// Check the response body
	expected := `{"status":"ok"}`
	// Trim new line from the end of the body
	if strings.TrimSpace(rr.Body.String()) != expected {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expected)
	}

	// Check if the log file was written to
	logFilePath := filepath.Join(testDir, "events.log")
	content, err := ioutil.ReadFile(logFilePath)
	if err != nil {
		t.Fatal("Failed to read log file:", err)
	}

	// The timestamp is dynamic, so we can't do an exact match.
	// Instead, we'll check if the log entry contains the expected fields.
	logString := string(content)
	if !strings.Contains(logString, "*****START*****") {
		t.Errorf("Log does not contain START marker")
	}
	if !strings.Contains(logString, "test-type") {
		t.Errorf("Log does not contain type")
	}
	if !strings.Contains(logString, "test-title") {
		t.Errorf("Log does not contain title")
	}
	if !strings.Contains(logString, "test message") {
		t.Errorf("Log does not contain message")
	}
	if !strings.Contains(logString, "*****END*****") {
		t.Errorf("Log does not contain END marker")
	}

	// Also check the timestamp format
	lines := strings.Split(logString, "\n")
	if len(lines) > 1 {
		parts := strings.Split(lines[1], " ")
		if len(parts) > 0 {
			_, err := time.Parse(time.RFC3339, parts[0])
			if err != nil {
				t.Errorf("Timestamp is not in the correct format: %v", err)
			}
		}
	}
}
