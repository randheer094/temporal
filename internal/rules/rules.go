// Package rules loads ingestion rules from YAML and applies them to incoming
// JSON payloads. A rule matches a request by host/path/method (plus an
// optional response status) and extracts fields using gjson paths embedded
// in template strings.
//
// Rule example:
//
//	rules:
//	  - name: user_login
//	    match:
//	      host: api.example.com   # optional, case-insensitive exact match
//	      path: /api/login        # exact, or trailing /* for prefix
//	      method: POST            # optional, case-insensitive
//	      status: "2xx"           # optional; "200", "2xx"/"4xx"/"5xx", or empty
//	    extract:
//	      type: "user_action"
//	      title: "Login: {request.body.user.name}"
//	      message: "{response.body.message}"
//
// A rule with `status` set only matches when the payload carries a response
// (i.e. a Proxyman onResponse forward). Bodies originating from onRequest
// match rules without a `status` constraint.
package rules

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"gopkg.in/yaml.v3"
)

type Match struct {
	Host   string `yaml:"host"`
	Path   string `yaml:"path"`
	Method string `yaml:"method"`
	Status string `yaml:"status"`
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

// Target is the set of values extracted from an incoming payload that a
// rule's Match block is checked against. StatusCode is 0 for request-only
// payloads.
type Target struct {
	Method     string
	Host       string
	Path       string
	StatusCode int
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

// Find returns the first rule that matches the target. Rules with a status
// constraint only match when t.StatusCode > 0.
func (rs *RuleSet) Find(t Target) *Rule {
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if !matchMethod(r.Match.Method, t.Method) {
			continue
		}
		if !matchHost(r.Match.Host, t.Host) {
			continue
		}
		if !matchPath(r.Match.Path, t.Path) {
			continue
		}
		if !matchStatus(r.Match.Status, t.StatusCode) {
			continue
		}
		return r
	}
	return nil
}

func matchMethod(pattern, method string) bool {
	if pattern == "" {
		return true
	}
	return strings.EqualFold(pattern, method)
}

func matchHost(pattern, host string) bool {
	if pattern == "" {
		return true
	}
	return strings.EqualFold(pattern, host)
}

func matchPath(pattern, path string) bool {
	if pattern == "" {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	return pattern == path
}

func matchStatus(pattern string, code int) bool {
	if pattern == "" {
		return true
	}
	if code == 0 {
		// Rule wants a status check but the payload has no response — skip.
		return false
	}
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if len(pattern) == 3 && strings.HasSuffix(pattern, "xx") {
		switch pattern[0] {
		case '1':
			return code >= 100 && code < 200
		case '2':
			return code >= 200 && code < 300
		case '3':
			return code >= 300 && code < 400
		case '4':
			return code >= 400 && code < 500
		case '5':
			return code >= 500 && code < 600
		}
		return false
	}
	var want int
	if _, err := fmt.Sscanf(pattern, "%d", &want); err != nil {
		return false
	}
	return want == code
}

// ExtractTarget pulls the matching values out of a JSON body, accommodating
// the various shapes a Proxyman script (onRequest or onResponse) or generic
// client might forward.
//
// Resolution order:
//  1. top-level `url` (full URL or absolute path) — provides host+path
//  2. top-level `host`/`path`
//  3. nested `request.host`/`request.path`, or `request.url`
//
// Method comes from top-level `method` or `request.method`. Status comes
// from top-level `status`/`statusCode` or `response.statusCode`.
func ExtractTarget(body []byte) Target {
	t := Target{
		Method: firstNonEmpty(
			gjson.GetBytes(body, "method").String(),
			gjson.GetBytes(body, "request.method").String(),
		),
		StatusCode: firstNonZero(
			int(gjson.GetBytes(body, "status").Int()),
			int(gjson.GetBytes(body, "statusCode").Int()),
			int(gjson.GetBytes(body, "response.statusCode").Int()),
		),
	}

	host, path := "", ""
	if u := gjson.GetBytes(body, "url").String(); u != "" {
		host, path = parseURL(u)
	}
	if host == "" {
		host = firstNonEmpty(
			gjson.GetBytes(body, "host").String(),
			gjson.GetBytes(body, "request.host").String(),
		)
	}
	if path == "" {
		path = firstNonEmpty(
			gjson.GetBytes(body, "path").String(),
			gjson.GetBytes(body, "request.path").String(),
		)
		if path == "" {
			if ru := gjson.GetBytes(body, "request.url").String(); ru != "" {
				if h, p := parseURL(ru); p != "" {
					if host == "" {
						host = h
					}
					path = p
				}
			}
		}
	}
	t.Host = host
	t.Path = path
	return t
}

// parseURL accepts either a full URL ("https://host/path?q=1") or a bare
// path ("/path?q=1") and returns host + path (query stripped).
func parseURL(s string) (host, path string) {
	u, err := url.Parse(s)
	if err != nil {
		return "", s
	}
	return u.Host, u.Path
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

var placeholder = regexp.MustCompile(`\{([^{}]+)\}`)

// Apply runs the rule's extract templates against the JSON body. {a.b.c}
// placeholders are replaced via gjson; missing paths render as empty
// strings. Strings without placeholders pass through.
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
