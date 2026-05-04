# Starlark Script Matching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `script:` field to rules that references a Starlark `.star` file in `~/.temporal/scripts/`; the script's `process(body)` function replaces the `each:`/`extract:` pipeline and returns a list of `{type, title, message}` log entries.

**Architecture:** `Rule` gains a `Script string` YAML field and a compiled `starlark.StringDict` stored after load. `Apply()` delegates to `applyScript()` when a compiled script is present. Script compilation happens in a new `CompileScripts(dir, logf)` method on `RuleSet`, called after `Load()` from both `api.go` and `cmd/rules.go`. All script errors go to `daemon.log`; the `/events` endpoint always returns 200.

**Tech Stack:** `go.starlark.net` (pure-Go Starlark interpreter), `encoding/json` (body→Starlark conversion), existing `filewriter` / signal reload infrastructure.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `go.mod` / `go.sum` | modify | add `go.starlark.net` |
| `internal/rules/rules.go` | modify | `Script` field on `Rule`; update `Apply` signature |
| `internal/rules/script.go` | create | `CompileScripts`, `applyScript`, `jsonToStarlark`, `starlarkToResults` |
| `internal/rules/rules_test.go` | modify | update `Apply(body)` → `Apply(body, nil)`; add script tests |
| `internal/api/api.go` | modify | call `rs.CompileScripts` in `ReloadRules`; pass `a.logDaemon` to `Apply` |
| `cmd/rules.go` | modify | call `rs.CompileScripts` after `Load` for CLI validation |
| `README.md` | modify | document `script:` field with example |

---

## Task 1: Add go.starlark.net dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Fetch the dependency**

```bash
cd /Users/rks/dev/Projects/temporal
go get go.starlark.net@latest
```

- [ ] **Step 2: Verify it was added**

```bash
grep starlark go.mod
```

Expected: a line like `go.starlark.net v0.0.0-...`

- [ ] **Step 3: Verify existing tests still pass**

```bash
go test ./...
```

Expected: all pass (no compilation errors).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add go.starlark.net"
```

---

## Task 2: Add Script field to Rule and update Apply signature

**Files:**
- Modify: `internal/rules/rules.go`
- Modify: `internal/rules/rules_test.go`
- Modify: `internal/api/api.go`

- [ ] **Step 1: Write the failing test for Script field parsing**

Add to the bottom of `internal/rules/rules_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./internal/rules/ -run TestScriptFieldParsedFromYAML -v
```

Expected: FAIL — `Script` field not defined.

- [ ] **Step 3: Add Script field to Rule struct and update Apply signature**

In `internal/rules/rules.go`, replace the `Rule` struct:

```go
type Rule struct {
	Name          string   `yaml:"name"`
	Matches       Matches  `yaml:"match"`
	Eaches        Eaches   `yaml:"each"`
	Extracts      Extracts `yaml:"extract"`
	Script        string   `yaml:"script"`
	scriptGlobals starlark.StringDict
}
```

Add the import at the top of the file (in the `import` block):

```go
"go.starlark.net/starlark"
```

Change `Apply`'s signature from:

```go
func (r *Rule) Apply(jsonBody []byte) []Result {
```

to:

```go
func (r *Rule) Apply(jsonBody []byte, logf func(string)) []Result {
```

No other change to the Apply body yet — leave the existing `each`/`extract` logic intact.

- [ ] **Step 4: Fix the existing test calls to Apply**

In `internal/rules/rules_test.go`, update every call from `r.Apply(body)` to `r.Apply(body, nil)`. There are 8 occurrences — replace all:

```
r.Apply(body)           →  r.Apply(body, nil)
r.Apply([]byte(...))    →  r.Apply([]byte(...), nil)
```

Affected test functions: `TestApplyRendersDeepPathsFromResponse`, `TestApplyMissingResponseDropsEmptyMessage`, `TestApplySkipsAllEmptyResult`, `TestApplyEachWithFilter`, `TestApplyEachMissingPathReturnsNothing`, `TestExtractListProducesOneEntryPerExtract`, `TestEachListUnionsContextBodies`, `TestEachListWithMultipleExtractsCartesians`.

- [ ] **Step 5: Fix the Apply call in api.go**

In `internal/api/api.go`, find `processItem` (line 173):

```go
results := rule.Apply(body)
```

Change to:

```go
results := rule.Apply(body, a.logDaemon)
```

- [ ] **Step 6: Run the new test and all existing tests**

```bash
go test ./internal/rules/ -run TestScriptFieldParsedFromYAML -v
go test ./...
```

Expected: `TestScriptFieldParsedFromYAML` passes, all others pass.

- [ ] **Step 7: Commit**

```bash
git add internal/rules/rules.go internal/rules/rules_test.go internal/api/api.go
git commit -m "feat: add Script field to Rule and update Apply signature"
```

---

## Task 3: JSON-to-Starlark conversion

**Files:**
- Create: `internal/rules/script.go`
- Modify: `internal/rules/rules_test.go`

- [ ] **Step 1: Write failing tests for jsonToStarlark**

Add to `internal/rules/rules_test.go`:

```go
func TestJsonToStarlark_Primitives(t *testing.T) {
	v, err := jsonToStarlark([]byte(`{"s":"hello","n":42,"f":3.14,"b":true,"null":null}`))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		t.Fatalf("expected *starlark.Dict, got %T", v)
	}
	check := func(key string, want string) {
		t.Helper()
		got, found, err := d.Get(starlark.String(key))
		if err != nil || !found {
			t.Errorf("key %q not found", key)
			return
		}
		if got.String() != want {
			t.Errorf("key %q: got %s, want %s", key, got.String(), want)
		}
	}
	check("s", `"hello"`)
	check("n", "42")
	check("b", "True")
	check("null", "None")
}

func TestJsonToStarlark_NestedArray(t *testing.T) {
	v, err := jsonToStarlark([]byte(`{"items":[{"name":"a"},{"name":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	d := v.(*starlark.Dict)
	items, found, _ := d.Get(starlark.String("items"))
	if !found {
		t.Fatal("items not found")
	}
	list, ok := items.(*starlark.List)
	if !ok {
		t.Fatalf("items is %T, want *starlark.List", items)
	}
	if list.Len() != 2 {
		t.Fatalf("items len = %d, want 2", list.Len())
	}
	first := list.Index(0).(*starlark.Dict)
	name, _, _ := first.Get(starlark.String("name"))
	if name.(starlark.String) != "a" {
		t.Errorf("first item name = %v, want a", name)
	}
}
```

Add the import `"go.starlark.net/starlark"` to `rules_test.go`'s import block.

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/rules/ -run TestJsonToStarlark -v
```

Expected: compilation error — `jsonToStarlark` not defined.

- [ ] **Step 3: Create internal/rules/script.go with jsonToStarlark**

```go
package rules

import (
	"encoding/json"
	"fmt"

	"go.starlark.net/starlark"
)

func jsonToStarlark(data []byte) (starlark.Value, error) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return interfaceToStarlark(v)
}

func interfaceToStarlark(v interface{}) (starlark.Value, error) {
	if v == nil {
		return starlark.None, nil
	}
	switch val := v.(type) {
	case bool:
		return starlark.Bool(val), nil
	case float64:
		if val == float64(int64(val)) {
			return starlark.MakeInt64(int64(val)), nil
		}
		return starlark.Float(val), nil
	case string:
		return starlark.String(val), nil
	case []interface{}:
		elems := make([]starlark.Value, len(val))
		for i, item := range val {
			sv, err := interfaceToStarlark(item)
			if err != nil {
				return nil, err
			}
			elems[i] = sv
		}
		return starlark.NewList(elems), nil
	case map[string]interface{}:
		d := new(starlark.Dict)
		for k, item := range val {
			sv, err := interfaceToStarlark(item)
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, fmt.Errorf("set key %q: %w", k, err)
			}
		}
		return d, nil
	default:
		return starlark.None, nil
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/rules/ -run TestJsonToStarlark -v
```

Expected: both pass.

- [ ] **Step 5: Commit**

```bash
git add internal/rules/script.go internal/rules/rules_test.go
git commit -m "feat: add jsonToStarlark conversion helper"
```

---

## Task 4: Starlark result conversion helpers

**Files:**
- Modify: `internal/rules/script.go`
- Modify: `internal/rules/rules_test.go`

- [ ] **Step 1: Write failing tests for starlarkToResults**

Add to `internal/rules/rules_test.go`:

```go
func makeStarlarkList(t *testing.T, entries ...map[string]starlark.Value) *starlark.List {
	t.Helper()
	var elems []starlark.Value
	for _, m := range entries {
		d := new(starlark.Dict)
		for k, v := range m {
			d.SetKey(starlark.String(k), v)
		}
		elems = append(elems, d)
	}
	return starlark.NewList(elems)
}

func TestStarlarkToResults_BasicEntry(t *testing.T) {
	list := makeStarlarkList(t, map[string]starlark.Value{
		"type":  starlark.String("info"),
		"title": starlark.String("hello"),
	})
	results := starlarkToResults("r", list, nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != "info" || results[0].Title != "hello" {
		t.Errorf("got %+v", results[0])
	}
}

func TestStarlarkToResults_MessageString(t *testing.T) {
	list := makeStarlarkList(t, map[string]starlark.Value{
		"type":    starlark.String("info"),
		"title":   starlark.String("t"),
		"message": starlark.String("single"),
	})
	results := starlarkToResults("r", list, nil)
	if len(results[0].Message) != 1 || results[0].Message[0] != "single" {
		t.Errorf("message = %v", results[0].Message)
	}
}

func TestStarlarkToResults_MessageList(t *testing.T) {
	msgs := starlark.NewList([]starlark.Value{starlark.String("a"), starlark.String("b")})
	list := makeStarlarkList(t, map[string]starlark.Value{
		"type":    starlark.String("info"),
		"title":   starlark.String("t"),
		"message": msgs,
	})
	results := starlarkToResults("r", list, nil)
	if len(results[0].Message) != 2 || results[0].Message[0] != "a" || results[0].Message[1] != "b" {
		t.Errorf("message = %v", results[0].Message)
	}
}

func TestStarlarkToResults_MissingTypeSkipped(t *testing.T) {
	var logged []string
	list := makeStarlarkList(t, map[string]starlark.Value{
		"title": starlark.String("t"),
	})
	results := starlarkToResults("r", list, func(msg string) { logged = append(logged, msg) })
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if len(logged) == 0 {
		t.Error("expected an error to be logged")
	}
}

func TestStarlarkToResults_MissingTitleSkipped(t *testing.T) {
	var logged []string
	list := makeStarlarkList(t, map[string]starlark.Value{
		"type": starlark.String("info"),
	})
	results := starlarkToResults("r", list, func(msg string) { logged = append(logged, msg) })
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if len(logged) == 0 {
		t.Error("expected an error to be logged")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/rules/ -run TestStarlarkToResults -v
```

Expected: FAIL — `starlarkToResults` not defined.

- [ ] **Step 3: Add starlarkToResults and helpers to script.go**

Append to `internal/rules/script.go`:

```go
func starlarkToResults(ruleName string, list *starlark.List, logf func(string)) []Result {
	var out []Result
	for i := 0; i < list.Len(); i++ {
		item := list.Index(i)
		d, ok := item.(*starlark.Dict)
		if !ok {
			if logf != nil {
				logf(fmt.Sprintf("rule %s: script entry %d is not a dict, skipping", ruleName, i))
			}
			continue
		}
		typVal, ok := starlarkDictString(d, "type")
		if !ok {
			if logf != nil {
				logf(fmt.Sprintf("rule %s: script entry %d missing or invalid 'type', skipping", ruleName, i))
			}
			continue
		}
		titleVal, ok := starlarkDictString(d, "title")
		if !ok {
			if logf != nil {
				logf(fmt.Sprintf("rule %s: script entry %d missing or invalid 'title', skipping", ruleName, i))
			}
			continue
		}
		out = append(out, Result{
			RuleName: ruleName,
			Type:     typVal,
			Title:    titleVal,
			Message:  starlarkDictMessages(d),
		})
	}
	return out
}

func starlarkDictString(d *starlark.Dict, key string) (string, bool) {
	v, found, err := d.Get(starlark.String(key))
	if err != nil || !found {
		return "", false
	}
	s, ok := v.(starlark.String)
	if !ok {
		return "", false
	}
	return string(s), true
}

func starlarkDictMessages(d *starlark.Dict) []string {
	v, found, _ := d.Get(starlark.String("message"))
	if !found {
		return nil
	}
	switch val := v.(type) {
	case starlark.String:
		s := string(val)
		if s == "" {
			return nil
		}
		return []string{s}
	case *starlark.List:
		var out []string
		for i := 0; i < val.Len(); i++ {
			if s, ok := val.Index(i).(starlark.String); ok {
				if str := string(s); str != "" {
					out = append(out, str)
				}
			}
		}
		return out
	default:
		return nil
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/rules/ -run TestStarlarkToResults -v
```

Expected: all 5 pass.

- [ ] **Step 5: Run full test suite**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/rules/script.go internal/rules/rules_test.go
git commit -m "feat: add starlarkToResults and dict helper functions"
```

---

## Task 5: Script compilation (CompileScripts)

**Files:**
- Modify: `internal/rules/script.go`
- Modify: `internal/rules/rules_test.go`

- [ ] **Step 1: Write a test helper for writing scripts to temp dirs**

Add to `internal/rules/rules_test.go`:

```go
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}
```

- [ ] **Step 2: Write failing tests for CompileScripts**

Add to `internal/rules/rules_test.go`:

```go
func TestCompileScripts_ValidScript(t *testing.T) {
	scriptBody := `
def process(body):
    return [{"type": "info", "title": "ok"}]
`
	dir := writeScript(t, "s.star", scriptBody)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: s.star
`))
	var logged []string
	rs.CompileScripts(dir, func(msg string) { logged = append(logged, msg) })
	if len(logged) != 0 {
		t.Errorf("unexpected log: %v", logged)
	}
	if rs.Rules[0].scriptGlobals == nil {
		t.Error("scriptGlobals should be set after compile")
	}
}

func TestCompileScripts_MissingFile(t *testing.T) {
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: missing.star
`))
	var logged []string
	rs.CompileScripts(t.TempDir(), func(msg string) { logged = append(logged, msg) })
	if len(logged) == 0 {
		t.Error("expected error logged for missing file")
	}
	if rs.Rules[0].scriptGlobals != nil {
		t.Error("scriptGlobals should be nil after failed compile")
	}
}

func TestCompileScripts_SyntaxError(t *testing.T) {
	dir := writeScript(t, "bad.star", `this is not valid starlark @@@@`)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: bad.star
`))
	var logged []string
	rs.CompileScripts(dir, func(msg string) { logged = append(logged, msg) })
	if len(logged) == 0 {
		t.Error("expected error logged for syntax error")
	}
	if rs.Rules[0].scriptGlobals != nil {
		t.Error("scriptGlobals should be nil after failed compile")
	}
}

func TestCompileScripts_NoProcessFunction(t *testing.T) {
	dir := writeScript(t, "noprocess.star", `x = 1`)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: noprocess.star
`))
	var logged []string
	rs.CompileScripts(dir, func(msg string) { logged = append(logged, msg) })
	if len(logged) == 0 {
		t.Error("expected error logged for missing process function")
	}
	if rs.Rules[0].scriptGlobals != nil {
		t.Error("scriptGlobals should be nil when process not defined")
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
	if rs.Rules[0].scriptGlobals != nil {
		t.Error("scriptGlobals should be nil for non-script rule")
	}
}
```

- [ ] **Step 3: Run to verify they fail**

```bash
go test ./internal/rules/ -run TestCompileScripts -v
```

Expected: FAIL — `CompileScripts` not defined.

- [ ] **Step 4: Implement compileScript and CompileScripts in script.go**

Add the import `"path/filepath"` to `script.go`'s import block. Then append to `internal/rules/script.go`:

```go
// CompileScripts reads and compiles the Starlark file for every rule that has
// a Script field. scriptsDir is the directory containing the .star files
// (typically ~/.temporal/scripts). Errors are sent to logf; failed rules have
// scriptGlobals left nil and are treated as no-match at runtime.
func (rs *RuleSet) CompileScripts(scriptsDir string, logf func(string)) {
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if r.Script == "" {
			continue
		}
		globals, err := compileScript(filepath.Join(scriptsDir, r.Script))
		if err != nil {
			if logf != nil {
				logf(fmt.Sprintf("script %s: compile error: %v", r.Script, err))
			}
			continue
		}
		if _, ok := globals["process"]; !ok {
			if logf != nil {
				logf(fmt.Sprintf("script %s: no 'process' function defined", r.Script))
			}
			continue
		}
		r.scriptGlobals = globals
	}
}

func compileScript(path string) (starlark.StringDict, error) {
	thread := &starlark.Thread{Name: path}
	return starlark.ExecFile(thread, path, nil, nil)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/rules/ -run TestCompileScripts -v
```

Expected: all 5 pass.

- [ ] **Step 6: Commit**

```bash
git add internal/rules/script.go internal/rules/rules_test.go
git commit -m "feat: implement CompileScripts for Starlark rule compilation"
```

---

## Task 6: applyScript, wire Apply, wire call sites

**Files:**
- Modify: `internal/rules/script.go`
- Modify: `internal/rules/rules.go`
- Modify: `internal/rules/rules_test.go`
- Modify: `internal/api/api.go`
- Modify: `cmd/rules.go`

- [ ] **Step 1: Write failing tests for applyScript (via Apply)**

Add to `internal/rules/rules_test.go`:

```go
func loadScriptRule(t *testing.T, scriptBody string) *Rule {
	t.Helper()
	dir := writeScript(t, "s.star", scriptBody)
	rs, err := Load(writeRules(t, `
rules:
  - name: scripted
    match: { path: /x }
    script: s.star
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
    fail("boom")
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
	dir := writeScript(t, "s.star", `
def process(body):
    return [{"type": "script", "title": "from-script"}]
`)
	rs, _ := Load(writeRules(t, `
rules:
  - name: r
    match: { path: /x }
    script: s.star
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
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/rules/ -run TestApplyScript -v
```

Expected: FAIL — `applyScript` not defined; `Apply` doesn't delegate yet.

- [ ] **Step 3: Add applyScript to script.go**

Append to `internal/rules/script.go`:

```go
func (r *Rule) applyScript(jsonBody []byte, logf func(string)) []Result {
	bodyVal, err := jsonToStarlark(jsonBody)
	if err != nil {
		if logf != nil {
			logf(fmt.Sprintf("rule %s: parse body: %v", r.Name, err))
		}
		return nil
	}
	thread := &starlark.Thread{Name: r.Name}
	fn := r.scriptGlobals["process"]
	result, err := starlark.Call(thread, fn, starlark.Tuple{bodyVal}, nil)
	if err != nil {
		if logf != nil {
			logf(fmt.Sprintf("rule %s: script runtime error: %v", r.Name, err))
		}
		return nil
	}
	list, ok := result.(*starlark.List)
	if !ok {
		if logf != nil {
			logf(fmt.Sprintf("rule %s: process() must return a list, got %s", r.Name, result.Type()))
		}
		return nil
	}
	return starlarkToResults(r.Name, list, logf)
}
```

- [ ] **Step 4: Wire Apply to delegate to applyScript**

In `internal/rules/rules.go`, update `Apply`:

```go
func (r *Rule) Apply(jsonBody []byte, logf func(string)) []Result {
	if r.scriptGlobals != nil {
		return r.applyScript(jsonBody, logf)
	}
	bodies := r.contextBodies(jsonBody)
	if len(r.Extracts) == 0 || len(bodies) == 0 {
		return nil
	}
	out := make([]Result, 0, len(bodies)*len(r.Extracts))
	for _, b := range bodies {
		for _, ex := range r.Extracts {
			res := Result{
				RuleName: r.Name,
				Type:     render(ex.Type, b),
				Title:    render(ex.Title, b),
				Message:  renderMessages(ex.Message, b),
			}
			if !res.Empty() {
				out = append(out, res)
			}
		}
	}
	return out
}
```

- [ ] **Step 5: Wire ReloadRules in api.go to call CompileScripts**

In `internal/api/api.go`, update `ReloadRules`:

```go
func (a *API) ReloadRules() error {
	rs, err := rules.Load(a.rulesPath)
	if err != nil {
		a.logDaemon("rules load failed: " + err.Error())
		a.rulesMu.Lock()
		if a.rules == nil {
			a.rules = &rules.RuleSet{}
		}
		a.rulesMu.Unlock()
		return err
	}
	rs.CompileScripts(filepath.Join(a.logDir, "scripts"), a.logDaemon)
	a.rulesMu.Lock()
	a.rules = rs
	a.rulesMu.Unlock()
	a.logDaemon(fmt.Sprintf("rules reloaded (%d rules)", len(rs.Rules)))
	return nil
}
```

- [ ] **Step 6: Wire refreshRules in cmd/rules.go to call CompileScripts**

In `cmd/rules.go`, update `refreshRules` after the `rules.Load` call:

```go
rs, err := rules.Load(rulesPath)
if err != nil {
    fmt.Fprintf(os.Stderr, "rules.yaml failed to parse: %v\n", err)
    os.Exit(1)
}

scriptsDir := filepath.Join(logDir, "scripts")
rs.CompileScripts(scriptsDir, func(msg string) {
    fmt.Fprintln(os.Stderr, "script warning:", msg)
})

fmt.Printf("rules.yaml OK — %d rule(s) loaded:\n", len(rs.Rules))
```

Also add `"path/filepath"` to the import block of `cmd/rules.go` (it already imports `"path/filepath"`, so this may already be present — verify with `go build`).

- [ ] **Step 7: Run all tests**

```bash
go test ./internal/rules/ -run TestApplyScript -v
go test ./...
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/rules/script.go internal/rules/rules.go internal/rules/rules_test.go internal/api/api.go cmd/rules.go
git commit -m "feat: implement Starlark script matching in rules"
```

---

## Task 7: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add script: field documentation to the Rule format section**

In `README.md`, find the existing rule format block that starts with:

```
#### Rule format
```

After the existing YAML example block (the `user_login` example), add the following new subsection **before** the `#### Match operators` heading:

```markdown
#### Script-based extraction

Instead of `each:` and `extract:`, a rule can delegate extraction to a [Starlark](https://github.com/google/starlark-go) script:

```yaml
rules:
  - name: my-rule
    match:
      host: api.example.com
      method: POST
    script: script1.star
```

`script:` is a filename resolved relative to `~/.temporal/scripts/`. When `script:` is present, `each:` and `extract:` are ignored — the script owns the full extraction pipeline.

The script must define a top-level function named `process` that accepts one argument (the event body as a Starlark dict) and returns a list of dicts:

```python
# ~/.temporal/scripts/script1.star

def process(body):
    entries = []
    for item in body.get("items", []):
        entries.append({
            "type": "info",
            "title": item["name"],
            "message": [item["status"]],
        })
    return entries
```

Each returned dict must have `type` and `title` (strings). `message` is optional — either a string or a list of strings. Entries missing required fields are skipped and logged to `daemon.log`.

Script errors (missing file, syntax error, runtime exception, bad return type) are all logged to `daemon.log`; the `/events` endpoint always returns `200`. Scripts are reloaded with `temporal server rules` — the same SIGHUP mechanism used for YAML rules.
```

- [ ] **Step 2: Update the Files table in the server section**

Find the Files table under `### Files` and add a new row:

```
- `~/.temporal/scripts/` — optional directory for Starlark extraction scripts referenced by `script:` fields in `rules.yaml`.
```

- [ ] **Step 3: Build and verify no errors**

```bash
go build ./...
```

Expected: clean build.

- [ ] **Step 4: Run full test suite one final time**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document script: field and Starlark extraction"
```

---

## Self-Review Checklist

- [x] **Spec coverage:** Script field + YAML parsing (Task 2) ✓ · jsonToStarlark (Task 3) ✓ · starlarkToResults (Task 4) ✓ · CompileScripts with all error cases (Task 5) ✓ · applyScript with all error cases + Apply delegation (Task 6) ✓ · call sites in api.go and cmd/rules.go (Task 6) ✓ · README (Task 7) ✓
- [x] **No placeholders:** All steps contain full code.
- [x] **Type consistency:** `Apply(jsonBody []byte, logf func(string)) []Result` used identically across rules.go, script.go, rules_test.go, and api.go. `CompileScripts(dir string, logf func(string))` matches across script.go, api.go, and cmd/rules.go. `scriptGlobals starlark.StringDict` accessed only inside rules package.
