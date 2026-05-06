package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// eventBody is a shared helper for tests that need a {type,title,message}
// payload. It pairs with writePassthroughRules.
type eventBody struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

// writePassthroughRules installs a rules.yaml that maps the simple
// {type,title,message} JSON shape straight into a log entry.
func writePassthroughRules(t *testing.T, dir string) {
	t.Helper()
	writeRules(t, dir, `
rules:
  - name: passthrough
    match: {}
    extract:
      type: "{type}"
      title: "{title}"
      message: "{message}"
`)
}

func TestLogEventHandler(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(eventBody{Type: "test-type", Title: "test-title", Message: "test message"})
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	a.Close()

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != `{"status":"ok"}` {
		t.Errorf("body = %q", rr.Body.String())
	}

	content, err := os.ReadFile(filepath.Join(dir, "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	entries := parseEventsLog(content)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Type != "test-type" || e.Title != "test-title" || len(e.Message) != 1 || e.Message[0] != "test message" {
		t.Errorf("entry = %+v", e)
	}
	if e.ID == "" || len(e.ID) < 4 {
		t.Errorf("missing/short ID: %q", e.ID)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Timestamp); err != nil {
		t.Errorf("timestamp not RFC3339Nano: %q (%v)", e.Timestamp, err)
	}
}

func TestLogEventHandlerWritesDaemonLog(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal("Failed to create API:", err)
	}

	body, _ := json.Marshal(eventBody{Type: "t", Title: "ti", Message: "m"})
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
	writePassthroughRules(t, dir)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal("Failed to create API:", err)
	}

	const n = 50
	for i := 0; i < n; i++ {
		body, _ := json.Marshal(eventBody{Type: "t", Title: fmt.Sprintf("title-%d", i), Message: "m"})
		req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
		a.logEventHandler(httptest.NewRecorder(), req)
	}
	a.Close()

	content, err := os.ReadFile(filepath.Join(dir, "events.log"))
	if err != nil {
		t.Fatal("Failed to read events log:", err)
	}
	entries := parseEventsLog(content)
	if len(entries) != n {
		t.Fatalf("got %d entries, want %d", len(entries), n)
	}
	for i, e := range entries {
		want := fmt.Sprintf("title-%d", i)
		if e.Title != want {
			t.Fatalf("entry %d: title = %q, want %q", i, e.Title, want)
		}
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

func TestNoRuleMatchDropsBody(t *testing.T) {
	// With no rules.yaml configured, every body should be dropped — no
	// fallback to a legacy shape.
	dir := t.TempDir()
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(eventBody{Type: "x", Title: "t", Message: "m"})
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()
	a.logEventHandler(rr, req)
	a.Close()

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != `{"status":"no_match"}` {
		t.Errorf("body = %q, want no_match", rr.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	if len(got) != 0 {
		t.Errorf("events.log should be empty, got: %s", got)
	}
}

func TestLogsJSONPaginationAndFilter(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, err := NewAPI(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		body, _ := json.Marshal(eventBody{Type: "alpha", Title: fmt.Sprintf("a%d", i), Message: "m"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Millisecond) // ensure timestamp ordering
	}
	for i := 0; i < 3; i++ {
		body, _ := json.Marshal(eventBody{Type: "beta", Title: fmt.Sprintf("b%d", i), Message: "m"})
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
	old := `{"id":"a1","timestamp":"2024-01-01T00:00:00Z","type":"legacy","title":"old-title","message":["old-msg"]}` + "\n"
	cur := `{"id":"a2","timestamp":"2025-01-01T00:00:00Z","type":"legacy","title":"new-title","message":["new-msg"]}` + "\n"
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

func TestTitleSearchFilter(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, _ := NewAPI(dir)
	titles := []string{"Login OK", "Login failed", "Order placed", "Cart cleared"}
	for _, title := range titles {
		body, _ := json.Marshal(eventBody{Type: "t", Title: title, Message: "x"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Microsecond)
	}
	a.Close()

	a2, _ := NewAPI(dir)
	defer a2.Close()
	rr := httptest.NewRecorder()
	a2.logsJSONHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.json?q=login", nil))
	var resp logsResponse
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2 for q=login", resp.Total)
	}
	for _, e := range resp.Entries {
		if !strings.Contains(strings.ToLower(e.Title), "login") {
			t.Errorf("unexpected match: %q", e.Title)
		}
	}
}

func TestDeleteSingleByID(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, _ := NewAPI(dir)
	for _, title := range []string{"a", "b", "c"} {
		body, _ := json.Marshal(eventBody{Type: "t", Title: title, Message: "m"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Microsecond)
	}
	a.Close()

	a2, _ := NewAPI(dir)
	defer a2.Close()
	entries := a2.readAllEvents()
	if len(entries) != 3 {
		t.Fatalf("setup: got %d entries", len(entries))
	}
	target := entries[1] // middle one (any will do)

	req := httptest.NewRequest(http.MethodDelete, "/logs/"+target.ID, nil)
	req.SetPathValue("id", target.ID)
	rr := httptest.NewRecorder()
	a2.deleteLogByIDHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	left := a2.readAllEvents()
	if len(left) != 2 {
		t.Fatalf("after delete: got %d entries, want 2", len(left))
	}
	for _, e := range left {
		if e.ID == target.ID {
			t.Errorf("deleted entry still present: %+v", e)
		}
	}

	// Deleting again returns 404.
	req2 := httptest.NewRequest(http.MethodDelete, "/logs/"+target.ID, nil)
	req2.SetPathValue("id", target.ID)
	rr2 := httptest.NewRecorder()
	a2.deleteLogByIDHandler(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", rr2.Code)
	}
}

func TestDeleteAllAndKeepWriting(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, _ := NewAPI(dir)
	for _, title := range []string{"a", "b", "c"} {
		body, _ := json.Marshal(eventBody{Type: "t", Title: title, Message: "m"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	}
	// Don't close — make sure delete works on a live writer and writes
	// continue to work afterward.
	rr := httptest.NewRecorder()
	a.deleteLogsHandler(rr, httptest.NewRequest(http.MethodDelete, "/logs", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rr.Code)
	}
	if entries := a.readAllEvents(); len(entries) != 0 {
		t.Errorf("after delete-all: got %d entries", len(entries))
	}

	// New writes should continue to work.
	body, _ := json.Marshal(eventBody{Type: "t", Title: "after", Message: "m"})
	a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	a.Close()

	entries := a.readAllEvents()
	if len(entries) != 1 || entries[0].Title != "after" {
		t.Errorf("post-delete write missing or wrong: %+v", entries)
	}
}

func TestDeleteWithTypeAndQueryFilter(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, _ := NewAPI(dir)
	type evt struct{ typ, title string }
	for _, e := range []evt{
		{"alpha", "Login OK"},
		{"alpha", "Login failed"},
		{"alpha", "Order placed"},
		{"beta", "Login error"},
	} {
		body, _ := json.Marshal(eventBody{Type: e.typ, Title: e.title, Message: "m"})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Microsecond)
	}

	// Delete alpha+login → should remove the two alpha login entries only.
	rr := httptest.NewRecorder()
	a.deleteLogsHandler(rr, httptest.NewRequest(http.MethodDelete, "/logs?type=alpha&q=login", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	a.Close()

	a2, _ := NewAPI(dir)
	defer a2.Close()
	entries := a2.readAllEvents()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	titles := []string{}
	for _, e := range entries {
		titles = append(titles, e.Title)
	}
	sort.Strings(titles)
	if titles[0] != "Login error" || titles[1] != "Order placed" {
		t.Errorf("survivors = %v, want [Login error, Order placed]", titles)
	}
}

func TestLogsCSVExport(t *testing.T) {
	dir := t.TempDir()
	writePassthroughRules(t, dir)
	a, _ := NewAPI(dir)
	type evt struct{ typ, title, msg string }
	for _, e := range []evt{
		{"alpha", "Login OK", "ip=1.2.3.4"},
		{"alpha", "Login failed", "ip=5.6.7.8"},
		{"beta", "Order placed", "id=42"},
	} {
		body, _ := json.Marshal(eventBody{Type: e.typ, Title: e.title, Message: e.msg})
		a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
		time.Sleep(time.Millisecond)
	}
	a.Close()

	a2, _ := NewAPI(dir)
	defer a2.Close()

	// No filter — should include all 3 entries.
	rr := httptest.NewRecorder()
	a2.logsCSVHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.csv", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv*", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Errorf("content-disposition = %q", cd)
	}
	out := rr.Body.String()
	if !strings.HasPrefix(out, "timestamp,type,title,message\n") {
		t.Errorf("missing header row, got: %q", out)
	}
	for _, want := range []string{"Login OK", "ip=1.2.3.4", "Login failed", "Order placed", "id=42"} {
		if !strings.Contains(out, want) {
			t.Errorf("CSV missing %q\n%s", want, out)
		}
	}
	// Header + 3 entries = 4 lines (each entry has a single message).
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Errorf("got %d lines, want 4:\n%s", len(lines), out)
	}

	// Filtered — type=alpha&q=login should yield exactly 2 entries.
	rr = httptest.NewRecorder()
	a2.logsCSVHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.csv?type=alpha&q=login", nil))
	out = rr.Body.String()
	lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Errorf("filtered: got %d lines, want 3:\n%s", len(lines), out)
	}
	if strings.Contains(out, "Order placed") {
		t.Errorf("filtered CSV should not include beta entry:\n%s", out)
	}
}

func TestLogsCSVExportExplodesMessages(t *testing.T) {
	// An entry with multiple message lines should produce one CSV row per
	// line, matching the user-requested format.
	dir := t.TempDir()
	writeRules(t, dir, `
rules:
  - name: multi
    match: { path: /api/x }
    extract:
      type: "user_action"
      title: "Login"
      message:
        - "ip={ip}"
        - "ua={ua}"
        - "session={sid}"
`)
	a, _ := NewAPI(dir)
	body := []byte(`{"url":"/api/x","ip":"1.2.3.4","ua":"curl","sid":"abc"}`)
	a.logEventHandler(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/events", bytes.NewBuffer(body)))
	a.Close()

	a2, _ := NewAPI(dir)
	defer a2.Close()
	rr := httptest.NewRecorder()
	a2.logsCSVHandler(rr, httptest.NewRequest(http.MethodGet, "/logs.csv", nil))
	out := rr.Body.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// header + 3 message rows
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), out)
	}
	for _, want := range []string{"ip=1.2.3.4", "ua=curl", "session=abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in CSV:\n%s", want, out)
		}
	}
	// Title and type should repeat on every row.
	loginCount := strings.Count(out, ",Login,")
	if loginCount != 3 {
		t.Errorf("title repeats = %d, want 3:\n%s", loginCount, out)
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
