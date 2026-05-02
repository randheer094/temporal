// Package rules loads ingestion rules from YAML and applies them to incoming
// JSON payloads. A rule matches a request by path (and optional method) and
// extracts fields using gjson paths embedded in template strings.
//
// Rule example:
//
//	rules:
//	  - name: user_login
//	    match:
//	      path: /ingest/login
//	      method: POST
//	    extract:
//	      type: "user_action"
//	      title: "Login: {user.name}"
//	      message: "{event.message} from {meta.ip}"
package rules

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"
)

type Match struct {
	Path   string `yaml:"path"`
	Method string `yaml:"method"`
}

type Extract struct {
	Type    string `yaml:"type"`
	Title   string `yaml:"title"`
	Message string `yaml:"message"`
}

type Rule struct {
	Name    string  `yaml:"name"`
	Match   Match   `yaml:"match"`
	Extract Extract `yaml:"extract"`
}

type RuleSet struct {
	Rules []Rule `yaml:"rules"`
}

type Result struct {
	RuleName string
	Type     string
	Title    string
	Message  string
}

// Load reads and parses a YAML rules file. A missing file returns an empty
// rule set so the daemon can start without one.
func Load(path string) (*RuleSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RuleSet{}, nil
		}
		return nil, fmt.Errorf("read rules %s: %w", path, err)
	}
	var rs RuleSet
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("parse rules %s: %w", path, err)
	}
	return &rs, nil
}

// Find returns the first rule that matches the given request method and path.
// A rule with an empty Method matches any method. A rule path ending in "/*"
// matches by prefix; otherwise the path must match exactly.
func (rs *RuleSet) Find(method, path string) *Rule {
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if r.Match.Method != "" && !strings.EqualFold(r.Match.Method, method) {
			continue
		}
		if matchPath(r.Match.Path, path) {
			return r
		}
	}
	return nil
}

func matchPath(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	return pattern == path
}

var placeholder = regexp.MustCompile(`\{([^{}]+)\}`)

// Apply runs the rule's extract templates against the JSON body and returns
// the resolved fields. {a.b.c} placeholders are replaced via gjson; missing
// paths render as empty strings. Strings without placeholders pass through.
func (r *Rule) Apply(jsonBody []byte) Result {
	return Result{
		RuleName: r.Name,
		Type:     render(r.Extract.Type, jsonBody),
		Title:    render(r.Extract.Title, jsonBody),
		Message:  render(r.Extract.Message, jsonBody),
	}
}

func render(tpl string, body []byte) string {
	if tpl == "" {
		return ""
	}
	return placeholder.ReplaceAllStringFunc(tpl, func(m string) string {
		path := m[1 : len(m)-1]
		return gjson.GetBytes(body, path).String()
	})
}
