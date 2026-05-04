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
      message:
        - "status={response.statusCode}"
        - "body={response.body.message}"
`))
	r := rs.Find(Target{Path: "/api/login"})
	body := []byte(`{"url":"/api/login","request":{"body":{"user":{"name":"jane"}}},"response":{"statusCode":200,"body":{"message":"hi"}}}`)
	results := r.Apply(body, nil)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.Title != "Login: jane" {
		t.Errorf("title = %q", got.Title)
	}
	if len(got.Message) != 2 || got.Message[0] != "status=200" || got.Message[1] != "body=hi" {
		t.Errorf("message = %v", got.Message)
	}
}

func TestApplyMissingResponseDropsEmptyMessage(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: api
    match: { path: /api/login }
    extract:
      type: "t"
      title: "{request.body.user.name}"
      message: "{response.body.message}"
`))
	r := rs.Find(Target{Path: "/api/login"})
	body := []byte(`{"url":"/api/login","request":{"body":{"user":{"name":"jane"}}}}`) // onRequest forward, no response
	results := r.Apply(body, nil)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Title != "jane" {
		t.Errorf("title = %q", results[0].Title)
	}
	// Empty rendered messages are dropped from the array.
	if len(results[0].Message) != 0 {
		t.Errorf("message = %v, want empty", results[0].Message)
	}
}

func TestApplySkipsAllEmptyResult(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: empty_when_missing
    match: { path: /api/x }
    extract:
      type: "{a.b}"
      title: "{c.d}"
      message: "{e.f}"
`))
	r := rs.Find(Target{Path: "/api/x"})
	results := r.Apply([]byte(`{"url":"/api/x"}`), nil)
	if len(results) != 0 {
		t.Fatalf("results = %d, want 0 for all-empty", len(results))
	}
}

func TestApplyEachWithFilter(t *testing.T) {
	rs, _ := Load(writeRules(t, `
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
`))
	r := rs.Find(Target{Path: "/api/cart"})
	body := []byte(`{
		"url": "/api/cart",
		"items": [
			{"id": 1, "name": "apple", "qty": 3},
			{"id": 2, "name": "low-stock alert: pear", "qty": 1},
			{"id": 3, "name": "banana", "qty": 4},
			{"id": 4, "name": "alert: kiwi", "qty": 2}
		]
	}`)
	results := r.Apply(body, nil)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2: %+v", len(results), results)
	}
	if results[0].Title != "low-stock alert: pear" || results[1].Title != "alert: kiwi" {
		t.Errorf("titles = %q, %q", results[0].Title, results[1].Title)
	}
	if len(results[0].Message) != 2 || results[0].Message[0] != "qty 1" || results[0].Message[1] != "id 2" {
		t.Errorf("first message = %v", results[0].Message)
	}
}

func TestApplyEachMissingPathReturnsNothing(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /api/x }
    each: items
    extract:
      type: t
      title: "{name}"
`))
	r := rs.Find(Target{Path: "/api/x"})
	if results := r.Apply([]byte(`{"url":"/api/x"}`), nil); len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestExtractAcceptsScalarMessage(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    extract:
      type: t
      title: T
      message: "single"
`))
	if got := rs.Rules[0].Extracts[0].Message; len(got) != 1 || got[0] != "single" {
		t.Errorf("message = %v", got)
	}
}

func TestMatchListAcceptsAnyAlternative(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: cart_or_order
    match:
      - { host: api.example.com, path: /api/cart }
      - { host: api.example.com, path: /api/order }
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(Target{Host: "api.example.com", Path: "/api/cart"}); r == nil {
		t.Fatal("expected match for /api/cart")
	}
	if r := rs.Find(Target{Host: "api.example.com", Path: "/api/order"}); r == nil {
		t.Fatal("expected match for /api/order")
	}
	if r := rs.Find(Target{Host: "api.example.com", Path: "/api/profile"}); r != nil {
		t.Fatal("/api/profile should not match either alternative")
	}
}

func TestExtractListProducesOneEntryPerExtract(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: dual
    match: { path: /x }
    extract:
      - { type: a, title: "A {n}", message: "ma" }
      - { type: b, title: "B {n}", message: "mb" }
`))
	r := rs.Find(Target{Path: "/x"})
	results := r.Apply([]byte(`{"url":"/x","n":"v"}`), nil)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Type != "a" || results[0].Title != "A v" {
		t.Errorf("first = %+v", results[0])
	}
	if results[1].Type != "b" || results[1].Title != "B v" {
		t.Errorf("second = %+v", results[1])
	}
}

func TestEachListUnionsContextBodies(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: cart_and_alerts
    match: { path: /x }
    each:
      - 'cart.items'
      - 'alerts'
    extract: { type: t, title: "{name}", message: "id {id}" }
`))
	r := rs.Find(Target{Path: "/x"})
	body := []byte(`{
		"url":"/x",
		"cart":{"items":[
			{"id":1,"name":"apple"},
			{"id":2,"name":"pear"}
		]},
		"alerts":[
			{"id":99,"name":"low-stock"}
		]
	}`)
	results := r.Apply(body, nil)
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	titles := []string{results[0].Title, results[1].Title, results[2].Title}
	want := []string{"apple", "pear", "low-stock"}
	for i, w := range want {
		if titles[i] != w {
			t.Errorf("results[%d].Title = %q, want %q", i, titles[i], w)
		}
	}
}

func TestEachListWithMultipleExtractsCartesians(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: combo
    match: { path: /x }
    each: ['items']
    extract:
      - { type: a, title: "A {name}" }
      - { type: b, title: "B {name}" }
`))
	r := rs.Find(Target{Path: "/x"})
	body := []byte(`{"url":"/x","items":[{"name":"foo"},{"name":"bar"}]}`)
	results := r.Apply(body, nil)
	// 2 items × 2 extracts = 4 entries
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	got := []string{}
	for _, r := range results {
		got = append(got, r.Type+":"+r.Title)
	}
	want := []string{"a:A foo", "b:B foo", "a:A bar", "b:B bar"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("results[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestEmptyMatchListMatchesEverything(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: catch_all
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(Target{Path: "/anything"}); r == nil {
		t.Fatal("rule with no match block should match anything")
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

func TestScriptFieldParsedFromYAML(t *testing.T) {
	rs, err := Load(writeRules(t, `
rules:
  - name: scripted
    match: { host: api.example.com }
    script: myscript.star
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rs.Rules))
	}
	if rs.Rules[0].Script != "myscript.star" {
		t.Errorf("Script = %q, want %q", rs.Rules[0].Script, "myscript.star")
	}
}
