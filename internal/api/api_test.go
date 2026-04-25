package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	a, err := NewAPI(testDir)
	if err != nil {
		t.Fatal("Failed to create API:", err)
	}

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
	a.logEventHandler(rr, req)

	// Flush queued writes before reading the file.
	a.Close()

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

func TestLogEventHandlerWritesDaemonLog(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal("Failed to create API:", err)
	}

	body, _ := json.Marshal(Event{Type: "t", Title: "ti", Message: "m"})
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
	a.logEventHandler(httptest.NewRecorder(), req)

	a.Close()

	content, err := os.ReadFile(filepath.Join(dir, "daemon.log"))
	if err != nil {
		t.Fatal("Failed to read daemon log:", err)
	}
	if !strings.Contains(string(content), "Received request on /events") {
		t.Errorf("daemon log missing expected entry: %q", string(content))
	}
}

func TestLogEventHandlerPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal("Failed to create API:", err)
	}

	const n = 50
	for i := 0; i < n; i++ {
		body, _ := json.Marshal(Event{Type: "t", Title: fmt.Sprintf("title-%d", i), Message: "m"})
		req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
		a.logEventHandler(httptest.NewRecorder(), req)
	}
	a.Close()

	content, err := os.ReadFile(filepath.Join(dir, "events.log"))
	if err != nil {
		t.Fatal("Failed to read events log:", err)
	}
	got := string(content)
	prev := -1
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "title-") {
			continue
		}
		var idx int
		if _, err := fmt.Sscanf(line, "title-%d", &idx); err != nil {
			t.Fatalf("could not parse title line %q: %v", line, err)
		}
		if idx != prev+1 {
			t.Fatalf("out-of-order entry: got title-%d after title-%d", idx, prev)
		}
		prev = idx
	}
	if prev != n-1 {
		t.Fatalf("expected %d entries, last seen title-%d", n, prev)
	}
}

func TestNewAPIErrorsOnBadLogDir(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatal("seed failed:", err)
	}
	if _, err := NewAPI(filepath.Join(notADir, "child")); err == nil {
		t.Fatal("expected error, got nil")
	}
}
