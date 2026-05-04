package rules

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requirePython skips a test when neither python3 nor python is available on
// PATH. Script-execution tests would otherwise fail in environments without
// a Python interpreter (some minimal CI images, sandboxes, etc.).
func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err == nil {
		return
	}
	if _, err := exec.LookPath("python"); err == nil {
		return
	}
	t.Skip("python3/python not available on PATH")
}

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

func TestFindQueryExactMatch(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: ref_homepage
    match:
      path: /api/items
      query:
        ref: homepage
    extract: { type: t, title: T, message: M }
`))
	body := []byte(`{"url":"https://x/api/items?ref=homepage&utm=foo"}`)
	if r := rs.Find(ExtractTarget(body)); r == nil {
		t.Fatal("expected match for ?ref=homepage")
	}
	wrongVal := []byte(`{"url":"https://x/api/items?ref=email"}`)
	if r := rs.Find(ExtractTarget(wrongVal)); r != nil {
		t.Fatal("?ref=email should not match ref: homepage")
	}
	missingKey := []byte(`{"url":"https://x/api/items?utm=foo"}`)
	if r := rs.Find(ExtractTarget(missingKey)); r != nil {
		t.Fatal("missing ref param should not match")
	}
	noQuery := []byte(`{"url":"https://x/api/items"}`)
	if r := rs.Find(ExtractTarget(noQuery)); r != nil {
		t.Fatal("missing query should not match")
	}
}

func TestFindQueryEmptyValueRequiresPresence(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: any_debug
    match:
      path: /api/items
      query:
        debug: ""
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/api/items?debug=1"}`))); r == nil {
		t.Fatal("debug=1 should match presence-only pattern")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/api/items?debug=verbose"}`))); r == nil {
		t.Fatal("debug=verbose should match presence-only pattern")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/api/items?debug"}`))); r == nil {
		t.Fatal("bare ?debug should match presence-only pattern")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/api/items"}`))); r != nil {
		t.Fatal("missing debug should not match presence-only pattern")
	}
}

// Rule constrains only the params it lists; extra params in the request are
// ignored. ?q1=v1 and ?q1=v1&q2=v2 must both match a rule that requires
// only q1=v1.
func TestFindQueryIgnoresExtraParams(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: only_q1
    match:
      path: /x
      query: { q1: v1 }
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?q1=v1"}`))); r == nil {
		t.Fatal("?q1=v1 should match rule requiring q1=v1")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?q1=v1&q2=v2"}`))); r == nil {
		t.Fatal("?q1=v1&q2=v2 should match rule requiring q1=v1 (extra params ignored)")
	}
}

func TestFindQueryAllKeysRequired(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match:
      path: /x
      query:
        a: "1"
        b: "2"
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?a=1&b=2"}`))); r == nil {
		t.Fatal("both params present should match")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?a=1"}`))); r != nil {
		t.Fatal("missing b should not match")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?a=1&b=3"}`))); r != nil {
		t.Fatal("wrong b value should not match")
	}
}

func TestFindQueryRepeatedParam(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match:
      path: /x
      query: { tag: "alert" }
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?tag=info&tag=alert"}`))); r == nil {
		t.Fatal("matching value among multiple should match")
	}
	if r := rs.Find(ExtractTarget([]byte(`{"url":"/x?tag=info&tag=warn"}`))); r != nil {
		t.Fatal("no matching value among multiple should not match")
	}
}

func TestExtractTargetQueryFromPathField(t *testing.T) {
	body := []byte(`{"path":"/api/items?ref=homepage","method":"GET"}`)
	tgt := ExtractTarget(body)
	if tgt.Path != "/api/items" {
		t.Errorf("path = %q, want /api/items", tgt.Path)
	}
	if got := tgt.Query.Get("ref"); got != "homepage" {
		t.Errorf("Query[ref] = %q, want homepage", got)
	}
}

func TestExtractTargetQueryFromRequestURL(t *testing.T) {
	body := []byte(`{"request":{"url":"https://x/api/items?ref=homepage"}}`)
	tgt := ExtractTarget(body)
	if got := tgt.Query.Get("ref"); got != "homepage" {
		t.Errorf("Query[ref] = %q, want homepage", got)
	}
}

func TestExtractTargetNoQueryYieldsNil(t *testing.T) {
	tgt := ExtractTarget([]byte(`{"url":"/api/items"}`))
	if tgt.Query != nil {
		t.Errorf("Query = %v, want nil", tgt.Query)
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

// --- active flag --------------------------------------------------------

func TestRuleDefaultsToActive(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    extract: { type: t, title: T, message: M }
`))
	if !rs.Rules[0].IsActive() {
		t.Fatal("rule with no active field should default to active=true")
	}
}

func TestRuleActiveFalseSkipped(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: disabled
    active: false
    match: { path: /x }
    extract: { type: t, title: T, message: M }
  - name: enabled
    active: true
    match: { path: /x }
    extract: { type: t2, title: T2, message: M2 }
`))
	r := rs.Find(Target{Path: "/x"})
	if r == nil {
		t.Fatal("expected enabled rule to match")
	}
	if r.Name != "enabled" {
		t.Errorf("Find returned %q, want %q (disabled rule should be skipped)", r.Name, "enabled")
	}
}

func TestRuleActiveFalseAloneNoMatch(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: only_disabled
    active: false
    match: { path: /x }
    extract: { type: t, title: T, message: M }
`))
	if r := rs.Find(Target{Path: "/x"}); r != nil {
		t.Fatalf("expected no match (rule disabled), got %q", r.Name)
	}
}

// --- script load / parse ------------------------------------------------

func TestScriptFieldParsedFromYAML(t *testing.T) {
	rs, err := Load(writeRules(t, `
rules:
  - name: scripted
    match: { host: api.example.com }
    script: myscript.py
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rs.Rules))
	}
	if rs.Rules[0].Script != "myscript.py" {
		t.Errorf("Script = %q, want %q", rs.Rules[0].Script, "myscript.py")
	}
}

func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCompileScripts_ValidScript(t *testing.T) {
	requirePython(t)
	dir := writeScript(t, "s.py", `
def process(body):
    return [{"type": "info", "title": "ok"}]
`)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: s.py
`))
	var logged []string
	rs.CompileScripts(dir, func(msg string) { logged = append(logged, msg) })
	if len(logged) != 0 {
		t.Errorf("unexpected log: %v", logged)
	}
	if rs.Rules[0].scriptPath == "" {
		t.Error("scriptPath should be set after compile")
	}
}

func TestCompileScripts_MissingFile(t *testing.T) {
	requirePython(t)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: missing.py
`))
	var logged []string
	rs.CompileScripts(t.TempDir(), func(msg string) { logged = append(logged, msg) })
	if len(logged) == 0 {
		t.Error("expected error logged for missing file")
	}
	if rs.Rules[0].scriptPath != "" {
		t.Error("scriptPath should be empty after failed compile")
	}
}

func TestCompileScripts_SyntaxError(t *testing.T) {
	requirePython(t)
	dir := writeScript(t, "bad.py", `def process(body)::::`)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: bad.py
`))
	var logged []string
	rs.CompileScripts(dir, func(msg string) { logged = append(logged, msg) })
	if len(logged) == 0 {
		t.Error("expected error logged for syntax error")
	}
	if rs.Rules[0].scriptPath != "" {
		t.Error("scriptPath should be empty after failed compile")
	}
}

func TestApply_ScriptCompileFailDoesNotFallThroughToExtract(t *testing.T) {
	requirePython(t)
	rs, err := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: missing.py
    extract:
      - type: t
        title: T
`))
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	rs.CompileScripts(t.TempDir(), func(msg string) { logged = append(logged, msg) })
	if rs.Rules[0].scriptPath != "" {
		t.Fatal("scriptPath should be empty after failed compile")
	}
	results := rs.Rules[0].Apply([]byte(`{"path":"/x"}`), nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results when script failed to compile, got %d", len(results))
	}
}

func TestCompileScripts_PathTraversalRejected(t *testing.T) {
	rs, err := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: ../evil.py
`))
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	rs.CompileScripts(t.TempDir(), func(msg string) { logged = append(logged, msg) })
	if rs.Rules[0].scriptPath != "" {
		t.Error("scriptPath should be empty for path-traversal script name")
	}
	if len(logged) == 0 {
		t.Error("expected a log message for invalid script filename")
	}
}

func TestCompileScripts_SkipsRulesWithoutScript(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    extract: { type: t, title: T }
`))
	rs.CompileScripts(t.TempDir(), nil)
	if rs.Rules[0].scriptPath != "" {
		t.Error("scriptPath should be empty for non-script rule")
	}
}

// --- script execution ---------------------------------------------------

func loadScriptRule(t *testing.T, scriptBody string) *Rule {
	t.Helper()
	requirePython(t)
	dir := writeScript(t, "s.py", scriptBody)
	rs, err := Load(writeRules(t, `
rules:
  - name: scripted
    match: { path: /x }
    script: s.py
`))
	if err != nil {
		t.Fatal(err)
	}
	rs.CompileScripts(dir, func(msg string) { t.Log("compile:", msg) })
	return &rs.Rules[0]
}

func TestApplyScript_ReturnsEntries(t *testing.T) {
	r := loadScriptRule(t, `
def process(body):
    return [
        {"type": "info", "title": body["host"], "message": [body["path"]]},
    ]
`)
	results := r.Apply([]byte(`{"host":"api.example.com","path":"/x"}`), nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != "info" || results[0].Title != "api.example.com" {
		t.Errorf("got %+v", results[0])
	}
	if len(results[0].Message) != 1 || results[0].Message[0] != "/x" {
		t.Errorf("message = %v", results[0].Message)
	}
}

func TestApplyScript_EmptyList(t *testing.T) {
	r := loadScriptRule(t, `
def process(body):
    return []
`)
	results := r.Apply([]byte(`{"host":"x","path":"/x"}`), nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestApplyScript_RuntimeErrorLogged(t *testing.T) {
	r := loadScriptRule(t, `
def process(body):
    raise Exception("boom")
`)
	var logged []string
	results := r.Apply([]byte(`{}`), func(msg string) { logged = append(logged, msg) })
	if len(results) != 0 {
		t.Errorf("expected 0 results on error, got %d", len(results))
	}
	if len(logged) == 0 {
		t.Error("expected runtime error to be logged")
	}
}

func TestApplyScript_BadReturnTypeLogged(t *testing.T) {
	r := loadScriptRule(t, `
def process(body):
    return "not a list"
`)
	var logged []string
	results := r.Apply([]byte(`{}`), func(msg string) { logged = append(logged, msg) })
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if len(logged) == 0 {
		t.Error("expected bad return type to be logged")
	}
}

func TestApplyScript_IgnoresEachAndExtract(t *testing.T) {
	requirePython(t)
	dir := writeScript(t, "s.py", `
def process(body):
    return [{"type": "script", "title": "from-script"}]
`)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: s.py
    each: items
    extract:
      type: extract
      title: from-extract
`))
	rs.CompileScripts(dir, nil)
	results := rs.Rules[0].Apply([]byte(`{"path":"/x","items":[{"name":"a"}]}`), nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result (script only), got %d", len(results))
	}
	if results[0].Type != "script" {
		t.Errorf("expected script result, got type=%q", results[0].Type)
	}
}

func TestApplyScript_MessageStringAndList(t *testing.T) {
	r := loadScriptRule(t, `
def process(body):
    return [
        {"type": "a", "title": "t1", "message": "single"},
        {"type": "b", "title": "t2", "message": ["x", "y"]},
    ]
`)
	results := r.Apply([]byte(`{}`), nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if len(results[0].Message) != 1 || results[0].Message[0] != "single" {
		t.Errorf("string message = %v", results[0].Message)
	}
	if len(results[1].Message) != 2 || results[1].Message[0] != "x" || results[1].Message[1] != "y" {
		t.Errorf("list message = %v", results[1].Message)
	}
}

// Scripts can use the full Python stdlib — sanity-check that json.loads
// works for callers parsing a string-valued response body (the original
// motivation for moving off Starlark).
func TestApplyScript_CanParseStringJSONBody(t *testing.T) {
	r := loadScriptRule(t, `
import json
def process(body):
    raw = body["response"]["body"]
    if isinstance(raw, str):
        raw = json.loads(raw)
    return [{"type": "ok", "title": raw["data"]["name"]}]
`)
	results := r.Apply([]byte(`{"response":{"body":"{\"data\":{\"name\":\"hello\"}}"}}`), nil)
	if len(results) != 1 || results[0].Title != "hello" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

// --- entriesToResults (no Python required) ------------------------------

func TestEntriesToResults_BasicEntry(t *testing.T) {
	out := entriesToResults("r", []map[string]any{
		{"type": "info", "title": "hello"},
	}, nil)
	if len(out) != 1 || out[0].Type != "info" || out[0].Title != "hello" {
		t.Errorf("got %+v", out)
	}
}

func TestEntriesToResults_MessageString(t *testing.T) {
	out := entriesToResults("r", []map[string]any{
		{"type": "info", "title": "t", "message": "single"},
	}, nil)
	if len(out[0].Message) != 1 || out[0].Message[0] != "single" {
		t.Errorf("message = %v", out[0].Message)
	}
}

func TestEntriesToResults_MessageList(t *testing.T) {
	out := entriesToResults("r", []map[string]any{
		{"type": "info", "title": "t", "message": []any{"a", "b"}},
	}, nil)
	if len(out[0].Message) != 2 || out[0].Message[0] != "a" || out[0].Message[1] != "b" {
		t.Errorf("message = %v", out[0].Message)
	}
}

func TestEntriesToResults_MissingTypeSkipped(t *testing.T) {
	var logged []string
	out := entriesToResults("r", []map[string]any{
		{"title": "t"},
	}, func(msg string) { logged = append(logged, msg) })
	if len(out) != 0 {
		t.Errorf("expected 0 results, got %d", len(out))
	}
	if len(logged) == 0 {
		t.Error("expected an error to be logged")
	}
}

func TestEntriesToResults_MissingTitleSkipped(t *testing.T) {
	var logged []string
	out := entriesToResults("r", []map[string]any{
		{"type": "info"},
	}, func(msg string) { logged = append(logged, msg) })
	if len(out) != 0 {
		t.Errorf("expected 0 results, got %d", len(out))
	}
	if len(logged) == 0 {
		t.Error("expected an error to be logged")
	}
}

