// Package rules loads ingestion rules from YAML and applies them to incoming
// JSON payloads. A rule matches a request by host/path/method/query (plus
// an optional response status) and extracts fields using gjson paths
// embedded in template strings. An optional `each` gjson path fans the rule
// out across nested array elements, so a body containing a list can produce
// one log entry per matching item.
//
// Rule example:
//
//	rules:
//	  - name: cart_alerts
//	    match:
//	      host: api.example.com
//	      path: /api/cart
//	      query:
//	        ref: homepage    # require ?ref=homepage
//	        debug: ""        # require ?debug present, any value
//	    each: 'items.#(name%"*alert*")#'   # one event per matching item
//	    extract:
//	      type: "cart_alert"
//	      title: "{name}"
//	      message:
//	        - "qty {qty}"
//	        - "id {id}"
//
// Without `each`, templates render once against the whole body. With `each`,
// each matched element becomes the body for template rendering.
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
	Host   string            `yaml:"host"`
	Path   string            `yaml:"path"`
	Method string            `yaml:"method"`
	Status string            `yaml:"status"`
	Query  map[string]string `yaml:"query"`
}

// Matches is the YAML form of a rule's match condition. It accepts either a
// single mapping or a sequence of mappings; multiple entries are OR-ed (rule
// fires if any one matches).
type Matches []Match

func (m *Matches) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case 0:
		return nil
	case yaml.MappingNode:
		var single Match
		if err := node.Decode(&single); err != nil {
			return err
		}
		*m = Matches{single}
		return nil
	case yaml.SequenceNode:
		var list []Match
		if err := node.Decode(&list); err != nil {
			return err
		}
		*m = Matches(list)
		return nil
	default:
		return fmt.Errorf("rules: match must be a mapping or list of mappings")
	}
}

// Eaches is the YAML form of a rule's `each` field. It accepts either a
// single gjson path string or a list. The fan-out unions every path's
// resolved elements into one stream of context bodies.
type Eaches []string

func (e *Eaches) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case 0:
		return nil
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		if s != "" {
			*e = Eaches{s}
		}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*e = Eaches(list)
		return nil
	default:
		return fmt.Errorf("rules: each must be a string or list of strings")
	}
}

// Extract holds the templates rendered for each event. Message is a list so
// rules can produce multi-line / multi-section log messages; the YAML field
// accepts either a single string or a list of strings.
type Extract struct {
	Type    string   `yaml:"type"`
	Title   string   `yaml:"title"`
	Message []string `yaml:"-"`
}

// extractRaw mirrors Extract for unmarshaling, treating Message as a generic
// node so we can accept either a scalar or a sequence.
type extractRaw struct {
	Type    string    `yaml:"type"`
	Title   string    `yaml:"title"`
	Message yaml.Node `yaml:"message"`
}

func (e *Extract) UnmarshalYAML(node *yaml.Node) error {
	var raw extractRaw
	if err := node.Decode(&raw); err != nil {
		return err
	}
	e.Type = raw.Type
	e.Title = raw.Title
	switch raw.Message.Kind {
	case 0:
		// no message field
	case yaml.ScalarNode:
		var s string
		if err := raw.Message.Decode(&s); err != nil {
			return err
		}
		if s != "" {
			e.Message = []string{s}
		}
	case yaml.SequenceNode:
		if err := raw.Message.Decode(&e.Message); err != nil {
			return err
		}
	default:
		return fmt.Errorf("rules: extract.message must be a string or a list")
	}
	return nil
}

// Extracts is the YAML form of a rule's `extract` field. It accepts either a
// single mapping or a sequence of mappings; each extract renders against
// every context body and produces its own log entry.
type Extracts []Extract

func (e *Extracts) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case 0:
		return nil
	case yaml.MappingNode:
		var single Extract
		if err := node.Decode(&single); err != nil {
			return err
		}
		*e = Extracts{single}
		return nil
	case yaml.SequenceNode:
		var list []Extract
		if err := node.Decode(&list); err != nil {
			return err
		}
		*e = Extracts(list)
		return nil
	default:
		return fmt.Errorf("rules: extract must be a mapping or list of mappings")
	}
}

type Rule struct {
	Name       string   `yaml:"name"`
	Active     *bool    `yaml:"active"`
	Matches    Matches  `yaml:"match"`
	Eaches     Eaches   `yaml:"each"`
	Extracts   Extracts `yaml:"extract"`
	Script     string   `yaml:"script"`
	scriptPath string
}

// IsActive reports whether the rule is enabled. The default is true; a rule
// is only treated as disabled when `active: false` is set explicitly.
func (r *Rule) IsActive() bool {
	return r.Active == nil || *r.Active
}

type RuleSet struct {
	Rules []Rule `yaml:"rules"`
}

// Target is the set of values extracted from an incoming payload that a
// rule's Match block is checked against. StatusCode is 0 for request-only
// payloads. Query is nil when the source URL had no query string.
type Target struct {
	Method     string
	Host       string
	Path       string
	StatusCode int
	Query      url.Values
}

// Result is one fully-rendered event produced by a rule.
type Result struct {
	RuleName string
	Type     string
	Title    string
	Message  []string
}

// Empty reports whether the rendered event has no content. Used by callers
// to skip writing a log entry that resolved to all-empty fields.
func (r Result) Empty() bool {
	if r.Type != "" || r.Title != "" {
		return false
	}
	for _, m := range r.Message {
		if m != "" {
			return false
		}
	}
	return true
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

// Find returns the first rule that matches the target. A rule with multiple
// match blocks fires if any one block matches (OR). A rule with no match
// block matches every target. Rules with a status constraint only match
// when t.StatusCode > 0. Rules with `active: false` are skipped.
func (rs *RuleSet) Find(t Target) *Rule {
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if !r.IsActive() {
			continue
		}
		if r.matchesTarget(t) {
			return r
		}
	}
	return nil
}

func (r *Rule) matchesTarget(t Target) bool {
	if len(r.Matches) == 0 {
		return true
	}
	for _, m := range r.Matches {
		if matchMethod(m.Method, t.Method) &&
			matchHost(m.Host, t.Host) &&
			matchPath(m.Path, t.Path) &&
			matchStatus(m.Status, t.StatusCode) &&
			matchQuery(m.Query, t.Query) {
			return true
		}
	}
	return false
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

// matchQuery checks every (key, value) pair in pattern against the request's
// query parameters. All keys must be present; if a pattern value is non-empty
// it must equal one of the values supplied for that key. An empty pattern
// value means "key must be present, value any".
func matchQuery(pattern map[string]string, query url.Values) bool {
	if len(pattern) == 0 {
		return true
	}
	for key, want := range pattern {
		values, ok := query[key]
		if !ok || len(values) == 0 {
			return false
		}
		if want == "" {
			continue
		}
		matched := false
		for _, v := range values {
			if v == want {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
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
//  1. top-level `url` (full URL or absolute path) — provides host+path+query
//  2. top-level `host`/`path` (path may carry a query string)
//  3. nested `request.host`/`request.path`, or `request.url`
//
// Method comes from top-level `method` or `request.method`. Status comes
// from top-level `status`/`statusCode` or `response.statusCode`. Query
// parameters come from the first source above that supplied them.
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
	var query url.Values
	if u := gjson.GetBytes(body, "url").String(); u != "" {
		host, path, query = parseURL(u)
	}
	if host == "" {
		host = firstNonEmpty(
			gjson.GetBytes(body, "host").String(),
			gjson.GetBytes(body, "request.host").String(),
		)
	}
	if path == "" {
		if raw := firstNonEmpty(
			gjson.GetBytes(body, "path").String(),
			gjson.GetBytes(body, "request.path").String(),
		); raw != "" {
			_, p, q := parseURL(raw)
			path = p
			if len(query) == 0 {
				query = q
			}
		}
		if path == "" {
			if ru := gjson.GetBytes(body, "request.url").String(); ru != "" {
				if h, p, q := parseURL(ru); p != "" {
					if host == "" {
						host = h
					}
					path = p
					if len(query) == 0 {
						query = q
					}
				}
			}
		}
	}
	t.Host = host
	t.Path = path
	t.Query = query
	return t
}

// parseURL accepts either a full URL ("https://host/path?q=1") or a bare
// path ("/path?q=1") and returns host, path, and parsed query parameters.
// Query is nil if the input had no query string.
func parseURL(s string) (host, path string, query url.Values) {
	u, err := url.Parse(s)
	if err != nil {
		return "", s, nil
	}
	q := u.Query()
	if len(q) == 0 {
		q = nil
	}
	return u.Host, u.Path, q
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

// Apply runs the rule against a JSON body and returns zero or more results.
//
// Context bodies (one or more) are determined by `each`:
//   - No `each`: the whole body is the single context.
//   - One or more `each` paths: each path's resolved elements (or single
//     object) are appended to a unioned stream of context bodies. Missing
//     paths contribute nothing.
//
// For every (context body × extract) pair, one Result is rendered. Empty
// Results (all fields rendered empty) are filtered out so the caller
// doesn't write blank log entries.
func (r *Rule) Apply(jsonBody []byte, logf func(string)) []Result {
	if r.Script != "" {
		if r.scriptPath == "" {
			return nil
		}
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

func (r *Rule) contextBodies(jsonBody []byte) [][]byte {
	if len(r.Eaches) == 0 {
		return [][]byte{jsonBody}
	}
	var out [][]byte
	for _, path := range r.Eaches {
		got := gjson.GetBytes(jsonBody, path)
		if !got.Exists() {
			continue
		}
		if got.IsArray() {
			got.ForEach(func(_, item gjson.Result) bool {
				out = append(out, []byte(item.Raw))
				return true
			})
		} else {
			out = append(out, []byte(got.Raw))
		}
	}
	return out
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

func renderMessages(tpls []string, body []byte) []string {
	if len(tpls) == 0 {
		return nil
	}
	out := make([]string, 0, len(tpls))
	for _, tpl := range tpls {
		s := render(tpl, body)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
