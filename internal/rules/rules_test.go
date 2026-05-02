package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRules(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	rs, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rs.Rules))
	}
}

func TestFindMethodAndPath(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: login
    match: { path: /api/login, method: POST }
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(Target{Method: "POST", Path: "/api/login"}); r == nil {
		t.Fatal("expected match")
	}
	if r := rs.Find(Target{Method: "GET", Path: "/api/login"}); r != nil {
		t.Fatal("GET should not match POST-only rule")
	}
}

func TestFindHostFilter(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: scoped
    match: { host: api.example.com, path: /v1/* }
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(Target{Host: "api.example.com", Path: "/v1/users/1"}); r == nil {
		t.Fatal("expected match")
	}
	if r := rs.Find(Target{Host: "other.com", Path: "/v1/users/1"}); r != nil {
		t.Fatal("different host should not match")
	}
}

func TestFindStatusPatterns(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: server_error
    match: { path: /api/*, status: "5xx" }
    extract: { type: e, title: T, message: M }
  - name: exact_201
    match: { path: /create, status: "201" }
    extract: { type: c, title: T, message: M }
`))
	if r := rs.Find(Target{Path: "/api/x", StatusCode: 503}); r == nil || r.Name != "server_error" {
		t.Fatalf("expected server_error, got %+v", r)
	}
	if r := rs.Find(Target{Path: "/api/x", StatusCode: 200}); r != nil {
		t.Fatal("200 should not match 5xx rule")
	}
	if r := rs.Find(Target{Path: "/create", StatusCode: 201}); r == nil {
		t.Fatal("expected 201 exact match")
	}
	// status set in rule but no response in payload -> no match
	if r := rs.Find(Target{Path: "/api/x", StatusCode: 0}); r != nil {
		t.Fatal("rule with status should not match when StatusCode=0")
	}
}

func TestExtractTargetFromFullURL(t *testing.T) {
	body := []byte(`{"url":"https://api.example.com/v1/users/42?ref=foo","method":"GET"}`)
	tgt := ExtractTarget(body)
	if tgt.Host != "api.example.com" || tgt.Path != "/v1/users/42" || tgt.Method != "GET" {
		t.Errorf("got %+v", tgt)
	}
}

func TestExtractTargetFromRequestObject(t *testing.T) {
	body := []byte(`{"request":{"host":"api.example.com","path":"/v1/orders","method":"POST"},"response":{"statusCode":201}}`)
	tgt := ExtractTarget(body)
	if tgt.Host != "api.example.com" || tgt.Path != "/v1/orders" || tgt.Method != "POST" || tgt.StatusCode != 201 {
		t.Errorf("got %+v", tgt)
	}
}

func TestExtractTargetFromRequestURL(t *testing.T) {
	body := []byte(`{"request":{"url":"https://api.example.com/v1/x","method":"GET"}}`)
	tgt := ExtractTarget(body)
	if tgt.Host != "api.example.com" || tgt.Path != "/v1/x" {
		t.Errorf("got %+v", tgt)
	}
}

func TestApplyRendersDeepPathsFromResponse(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: api
    match: { path: /api/login }
    extract:
      type: "user"
      title: "Login: {request.body.user.name}"
      message: "status={response.statusCode} body={response.body.message}"
`))
	r := rs.Find(Target{Path: "/api/login"})
	body := []byte(`{"url":"/api/login","request":{"body":{"user":{"name":"jane"}}},"response":{"statusCode":200,"body":{"message":"hi"}}}`)
	got := r.Apply(body)
	if got.Title != "Login: jane" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "status=200 body=hi" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestApplyMissingResponseRendersEmpty(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: api
    match: { path: /api/login }
    extract:
      type: "t"
      title: "{request.body.user.name}"
      message: "status={response.statusCode}"
`))
	r := rs.Find(Target{Path: "/api/login"})
	body := []byte(`{"url":"/api/login","request":{"body":{"user":{"name":"jane"}}}}`) // onRequest forward, no response
	got := r.Apply(body)
	if got.Title != "jane" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "status=" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestPathPrefixWithQueryString(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: items
    match: { path: /items/* }
    extract: { type: t, title: T, message: M }
`))
	body := []byte(`{"url":"https://x/items/42?ref=q"}`)
	tgt := ExtractTarget(body)
	if tgt.Path != "/items/42" {
		t.Fatalf("path = %q", tgt.Path)
	}
	if rs.Find(tgt) == nil {
		t.Fatal("expected match")
	}
}
