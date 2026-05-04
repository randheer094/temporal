package rules

import (
	"encoding/json"
	"fmt"
	"path/filepath"

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
