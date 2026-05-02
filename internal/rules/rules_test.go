package rules

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleYAML = `
rules:
  - name: user_login
    match:
      path: /ingest/login
      method: POST
    extract:
      type: "user_action"
      title: "Login: {user.name}"
      message: "{event.message} from {meta.ip}"
  - name: any_method
    match:
      path: /ingest/anything
    extract:
      type: "generic"
      title: "{title}"
      message: "{body}"
  - name: prefix_rule
    match:
      path: /ingest/items/*
    extract:
      type: "item"
      title: "Item {id}"
      message: "{nested.deep.value}"
`

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

func TestFindExactPathAndMethod(t *testing.T) {
	rs, err := Load(writeRules(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	r := rs.Find("POST", "/ingest/login")
	if r == nil || r.Name != "user_login" {
		t.Fatalf("expected user_login, got %+v", r)
	}
	if rs.Find("GET", "/ingest/login") != nil {
		t.Fatal("GET should not match POST-only rule")
	}
}

func TestFindAnyMethodWhenUnset(t *testing.T) {
	rs, _ := Load(writeRules(t, sampleYAML))
	if r := rs.Find("DELETE", "/ingest/anything"); r == nil || r.Name != "any_method" {
		t.Fatalf("expected any_method, got %+v", r)
	}
}

func TestFindPrefixMatch(t *testing.T) {
	rs, _ := Load(writeRules(t, sampleYAML))
	if r := rs.Find("POST", "/ingest/items/42"); r == nil || r.Name != "prefix_rule" {
		t.Fatalf("expected prefix_rule, got %+v", r)
	}
	if r := rs.Find("POST", "/ingest/items"); r == nil {
		t.Fatal("bare prefix should match")
	}
	if r := rs.Find("POST", "/ingest/itemsxyz"); r != nil {
		t.Fatal("non-segment prefix should not match")
	}
}

func TestApplyRendersDeepPaths(t *testing.T) {
	rs, _ := Load(writeRules(t, sampleYAML))
	r := rs.Find("POST", "/ingest/login")
	body := []byte(`{"user":{"name":"jane"},"event":{"message":"hi"},"meta":{"ip":"1.2.3.4"}}`)
	got := r.Apply(body)
	if got.Title != "Login: jane" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "hi from 1.2.3.4" {
		t.Errorf("message = %q", got.Message)
	}
	if got.Type != "user_action" {
		t.Errorf("type = %q", got.Type)
	}
}

func TestApplyMissingPathBecomesEmpty(t *testing.T) {
	rs, _ := Load(writeRules(t, sampleYAML))
	r := rs.Find("POST", "/ingest/items/1")
	got := r.Apply([]byte(`{"id":"abc"}`))
	if got.Title != "Item abc" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "" {
		t.Errorf("message = %q, want empty", got.Message)
	}
}

func TestApplyArrayIndexAndQuery(t *testing.T) {
	yamlBody := `
rules:
  - name: first_item
    match: { path: /ingest/order }
    extract:
      type: "order"
      title: "First: {items.0.name}"
      message: "Adult: {users.#(age>18).name}"
`
	rs, _ := Load(writeRules(t, yamlBody))
	r := rs.Find("POST", "/ingest/order")
	body := []byte(`{"items":[{"name":"apple"},{"name":"pear"}],"users":[{"name":"kid","age":10},{"name":"adult","age":30}]}`)
	got := r.Apply(body)
	if got.Title != "First: apple" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "Adult: adult" {
		t.Errorf("message = %q", got.Message)
	}
}
