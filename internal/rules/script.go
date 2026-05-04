package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Python interpreter discovery: tried once per process. Both python3 and
// python are accepted so the daemon runs on systems that ship one or the
// other (modern Linux/macOS use python3; some BSDs only have python).
var (
	pythonOnce sync.Once
	pythonBin  string
	pythonErr  error
)

// scriptTimeout caps wall-clock time for any single process(body) invocation.
// Long-running scripts are killed and logged; the /events handler still
// returns 200 so a buggy script can't take down ingestion.
const scriptTimeout = 5 * time.Second

// pythonRunner is a tiny stdin/stdout shim that loads the user's script as
// a Python module, calls process(body) with the JSON body parsed from stdin,
// and writes the returned list of dicts as JSON to stdout. Keeping this
// inline (rather than shipping a separate runner.py file) means scripts work
// with any temporal binary, no install-time file layout to get wrong.
const pythonRunner = `
import json, sys, runpy
ns = runpy.run_path(sys.argv[1])
process = ns.get("process")
if not callable(process):
    sys.stderr.write("no callable 'process(body)' defined\n")
    sys.exit(2)
body = json.load(sys.stdin)
entries = process(body)
json.dump(entries, sys.stdout)
`

func detectPython() (string, error) {
	pythonOnce.Do(func() {
		for _, name := range []string{"python3", "python"} {
			if p, err := exec.LookPath(name); err == nil {
				pythonBin = p
				return
			}
		}
		pythonErr = errors.New("no python3 or python found on PATH")
	})
	return pythonBin, pythonErr
}

// CompileScripts resolves each rule's script filename against scriptsDir and
// validates Python syntax with a one-shot `ast.parse` so syntax errors show
// up at load time instead of on the first matching event. Rules whose
// script fails validation have scriptPath left blank, which Apply treats as
// "skip" — the failure is logged via logf for the daemon's main log.
func (rs *RuleSet) CompileScripts(scriptsDir string, logf func(string)) {
	for i := range rs.Rules {
		r := &rs.Rules[i]
		if r.Script == "" {
			continue
		}
		if filepath.Base(r.Script) != r.Script {
			if logf != nil {
				logf(fmt.Sprintf("script %s: invalid filename (must be a bare filename, not a path)", r.Script))
			}
			continue
		}
		fullPath := filepath.Join(scriptsDir, r.Script)
		if err := validatePythonSyntax(fullPath); err != nil {
			if logf != nil {
				logf(fmt.Sprintf("script %s: %v", r.Script, err))
			}
			continue
		}
		r.scriptPath = fullPath
	}
}

func validatePythonSyntax(path string) error {
	py, err := detectPython()
	if err != nil {
		return err
	}
	cmd := exec.Command(py, "-c",
		"import ast,sys; ast.parse(open(sys.argv[1]).read(), filename=sys.argv[1])",
		path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			return fmt.Errorf("syntax check failed: %w", err)
		}
		return fmt.Errorf("syntax check failed: %s", strings.TrimSpace(msg))
	}
	return nil
}

func (r *Rule) applyScript(jsonBody []byte, logf func(string)) []Result {
	py, err := detectPython()
	if err != nil {
		if logf != nil {
			logf(fmt.Sprintf("rule %s: %v", r.Name, err))
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, py, "-c", pythonRunner, r.scriptPath)
	cmd.Stdin = bytes.NewReader(jsonBody)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if logf != nil {
			detail := strings.TrimSpace(stderr.String())
			if ctx.Err() == context.DeadlineExceeded {
				logf(fmt.Sprintf("rule %s: script timed out after %s", r.Name, scriptTimeout))
			} else if detail != "" {
				logf(fmt.Sprintf("rule %s: script error: %v: %s", r.Name, err, detail))
			} else {
				logf(fmt.Sprintf("rule %s: script error: %v", r.Name, err))
			}
		}
		return nil
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return nil
	}
	var entries []map[string]any
	if err := json.Unmarshal(out, &entries); err != nil {
		if logf != nil {
			logf(fmt.Sprintf("rule %s: process() must return a list of dicts (got: %s)", r.Name, err))
		}
		return nil
	}
	return entriesToResults(r.Name, entries, logf)
}

func entriesToResults(ruleName string, entries []map[string]any, logf func(string)) []Result {
	var out []Result
	for i, e := range entries {
		typ, ok := e["type"].(string)
		if !ok || typ == "" {
			if logf != nil {
				logf(fmt.Sprintf("rule %s: script entry %d missing or invalid 'type', skipping", ruleName, i))
			}
			continue
		}
		title, ok := e["title"].(string)
		if !ok || title == "" {
			if logf != nil {
				logf(fmt.Sprintf("rule %s: script entry %d missing or invalid 'title', skipping", ruleName, i))
			}
			continue
		}
		out = append(out, Result{
			RuleName: ruleName,
			Type:     typ,
			Title:    title,
			Message:  entryMessages(e["message"]),
		})
	}
	return out
}

func entryMessages(v any) []string {
	switch m := v.(type) {
	case string:
		if m == "" {
			return nil
		}
		return []string{m}
	case []any:
		var out []string
		for _, item := range m {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
