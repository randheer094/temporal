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
	"sort"
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

func writeRules(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "rules.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRuleBasedEventByURL(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: user_login
    match: { path: /api/login, method: POST }
    extract:
      type: "user_action"
      title: "Login: {user.name}"
      message: "{event.message} from {meta.ip}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"url":"/api/login","method":"POST","user":{"name":"jane"},"event":{"message":"hi"},"meta":{"ip":"1.2.3.4"}}`)
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, req)
	a.Close()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{"user_action", "Login: jane", "hi from 1.2.3.4"} {
		if !strings.Contains(s, want) {
			t.Errorf("log missing %q\n%s", want, s)
		}
	}
}

func TestArrayBodyProducesMultipleEvents(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: order_item
    match: { path: /api/order }
    extract:
      type: "order"
      title: "Item {name}"
      message: "qty {qty}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`[
		{"url":"/api/order","name":"apple","qty":3},
		{"url":"/api/order","name":"pear","qty":7}
	]`)
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, req)
	a.Close()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	s := string(got)
	if !strings.Contains(s, "Item apple") || !strings.Contains(s, "qty 3") {
		t.Errorf("missing first item:\n%s", s)
	}
	if !strings.Contains(s, "Item pear") || !strings.Contains(s, "qty 7") {
		t.Errorf("missing second item:\n%s", s)
	}
}

func TestLegacyEventFallbackWhenNoRuleMatches(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(Event{Type: "legacy", Title: "t", Message: "m"})
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, req)
	a.Close()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	if !strings.Contains(string(got), "legacy") {
		t.Errorf("legacy event not written:\n%s", string(got))
	}
}

func TestLogsJSONPaginationAndFilter(t *testing.T) {
	dir := t.TempDir()
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		body, _ := json.Marshal(Event{Type: "alpha", Title: fmt.Sprintf("a%d", i), Message: "m"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Millisecond) // ensure timestamp ordering
	}
	for i := 0; i < 3; i++ {
		body, _ := json.Marshal(Event{Type: "beta", Title: fmt.Sprintf("b%d", i), Message: "m"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Millisecond)
	}
	a.Close()

	// Reopen so the read sees the flushed file.
	a2, _ := NewAPI(dir)
	defer a2.Close()

	rr := httptest.NewRecorder()
	a2.logsJSONHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.json?page=1&size=10", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp logsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 8 {
		t.Errorf("total = %d, want 8", resp.Total)
	}
	if len(resp.Types) != 2 {
		t.Errorf("types = %v, want [alpha beta]", resp.Types)
	}

	rr = httptest.NewRecorder()
	a2.logsJSONHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.json?type=beta&size=2&page=1", nil))
	var filtered logsResponse
	json.Unmarshal(rr.Body.Bytes(), &filtered)
	if filtered.Total != 3 {
		t.Errorf("filtered total = %d, want 3", filtered.Total)
	}
	if len(filtered.Entries) != 2 {
		t.Errorf("page size = %d, want 2", len(filtered.Entries))
	}
	if filtered.TotalPages != 2 {
		t.Errorf("total_pages = %d, want 2", filtered.TotalPages)
	}
}

func TestLogsJSONReadsRotatedBackup(t *testing.T) {
	dir := t.TempDir()
	// Seed an old (rotated) log alongside a current one.
	old := "*****START*****\n2024-01-01T00:00:00Z legacy\nold-title\nold-msg\n*****END*****\n"
	cur := "*****START*****\n2025-01-01T00:00:00Z legacy\nnew-title\nnew-msg\n*****END*****\n"
	os.WriteFile(filepath.Join(dir, "events.log.1"), []byte(old), 0644)
	os.WriteFile(filepath.Join(dir, "events.log"), []byte(cur), 0644)

	a, _ := NewAPI(dir)
	defer a.Close()

	rr := httptest.NewRecorder()
	a.logsJSONHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.json", nil))
	var resp logsResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2", resp.Total)
	}
	// Newest first.
	if resp.Entries[0].Title != "new-title" {
		t.Errorf("first entry = %q, want new-title", resp.Entries[0].Title)
	}
}

func TestProxymanFullURLAndHostMatch(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: example_login
    match:
      host: api.example.com
      path: /api/login
      method: POST
    extract:
      type: "auth"
      title: "{request.method} {request.path}"
      message: "user={request.body.user.name}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"url": "https://api.example.com/api/login?ref=abc",
		"method": "POST",
		"request": {
			"host": "api.example.com",
			"path": "/api/login",
			"method": "POST",
			"body": {"user": {"name": "jane"}}
		}
	}`)
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	a.Close()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	s := string(got)
	for _, want := range []string{"auth", "POST /api/login", "user=jane"} {
		if !strings.Contains(s, want) {
			t.Errorf("log missing %q\n%s", want, s)
		}
	}
}

func TestEventsAlwaysReturns200(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: scoped
    match: { host: api.example.com, path: /v1/x }
    extract: { type: t, title: T, message: M }
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	cases := [][]byte{
		[]byte(`{"url":"https://other.com/v1/x"}`), // no rule match
		[]byte(`not even json`),                    // invalid JSON
		[]byte(`{}`),                               // empty object
		[]byte(`[]`),                               // empty array
	}
	for _, body := range cases {
		rr := httptest.NewRecorder()
		a.logEventHandler(rr, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		if rr.Code != http.StatusOK {
			t.Errorf("body %q: status = %d, want 200", string(body), rr.Code)
		}
	}
	a.Close()

	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	if len(got) != 0 {
		t.Errorf("events.log should be empty, got: %s", got)
	}
}

func TestProxymanResponseStatusFilter(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: errors_only
    match: { path: /api/*, status: "5xx" }
    extract:
      type: "error"
      title: "{response.statusCode} {request.path}"
      message: "{response.body.error}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 200 -> should not log (no rule matches).
	ok := []byte(`{"url":"/api/x","request":{"path":"/api/x","method":"GET"},"response":{"statusCode":200,"body":{}}}`)
	a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(ok)))

	// 503 -> should log.
	bad := []byte(`{"url":"/api/x","request":{"path":"/api/x","method":"GET"},"response":{"statusCode":503,"body":{"error":"oops"}}}`)
	a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(bad)))

	a.Close()
	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	s := string(got)
	if strings.Contains(s, "200 ") {
		t.Errorf("200 should not have logged: %s", s)
	}
	if !strings.Contains(s, "503 /api/x") || !strings.Contains(s, "oops") {
		t.Errorf("503 not logged correctly: %s", s)
	}
}

func TestProxymanOnRequestForward(t *testing.T) {
	// onRequest payload has no response; rules without a status constraint
	// must still match.
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: with_status
    match: { path: /api/*, status: "2xx" }
    extract: { type: ok, title: T, message: M }
  - name: any
    match: { path: /api/* }
    extract:
      type: "request"
      title: "{request.method} {request.path}"
      message: "body={request.body.x}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"url":"https://api.example.com/api/foo","request":{"path":"/api/foo","method":"GET","body":{"x":42}}}`)
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	a.Close()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	s := string(got)
	if !strings.Contains(s, "request") || !strings.Contains(s, "GET /api/foo") || !strings.Contains(s, "body=42") {
		t.Errorf("missing onRequest log:\n%s", s)
	}
	// status-required rule should NOT have fired.
	if strings.Contains(s, "ok\nT\nM") {
		t.Errorf("status-required rule fired without response:\n%s", s)
	}
}

func TestEachFanOutWithFilterAndArrayMessage(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: cart_alerts
    match: { path: /api/cart }
    each: 'items.#(name%"*alert*")#'
    extract:
      type: "alert"
      title: "{name}"
      message:
        - "qty {qty}"
        - "id {id}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"url":"/api/cart",
		"items":[
			{"id":1,"name":"apple","qty":3},
			{"id":2,"name":"alert: pear","qty":1},
			{"id":3,"name":"banana","qty":4},
			{"id":4,"name":"alert: kiwi","qty":2}
		]
	}`)
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	a.Close()

	// Verify two entries via the JSON viewer; messages should be arrays.
	a2, _ := NewAPI(dir)
	defer a2.Close()
	rr = httptest.NewRecorder()
	a2.logsJSONHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.json", nil))
	var resp logsResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Total)
	}
	titles := []string{resp.Entries[0].Title, resp.Entries[1].Title}
	sort.Strings(titles)
	if titles[0] != "alert: kiwi" || titles[1] != "alert: pear" {
		t.Errorf("titles = %v", titles)
	}
	for _, e := range resp.Entries {
		if len(e.Message) != 2 {
			t.Errorf("entry %q message len = %d, want 2 (%v)", e.Title, len(e.Message), e.Message)
		}
	}
}

func TestEmptyExtractIsNotLogged(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: empty_when_missing
    match: { path: /api/x }
    extract:
      type: "{a.b}"
      title: "{c.d}"
      message: "{e.f}"
`)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	body := []byte(`{"url":"/api/x"}`)
	a.logEventHandler(rr, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	a.Close()

	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	if len(got) != 0 {
		t.Errorf("expected no log written, got: %s", got)
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
