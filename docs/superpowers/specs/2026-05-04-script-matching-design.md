# Script Matching in Rules

**Date:** 2026-05-04  
**Status:** Approved

## Overview

Add support for Starlark scripts as an alternative extraction pipeline in rules. A rule can reference a `.star` file in `~/.temporal/scripts/` via a `script:` field. When present, the script replaces the `each:`/`extract:` pipeline — the script receives the matched event body and returns a list of log entries.

## Rule YAML

The `script:` field is optional and mutually exclusive with `each:` and `extract:`. The filename is resolved relative to `~/.temporal/scripts/`.

```yaml
rules:
  - name: my-api-rule
    match:
      host: api.example.com
      method: POST
    script: script1.star
```

All existing `match:` conditions work unchanged. If `script:` is present, `each:` and `extract:` are ignored even if specified.

## Script Convention

Scripts live in `~/.temporal/scripts/`. Each script must define a top-level function named `process` that accepts one argument (the event body as a Starlark dict) and returns a list of dicts.

```python
# ~/.temporal/scripts/script1.star

def process(body):
    entries = []
    for item in body.get("items", []):
        entries.append({
            "type": "info",
            "title": item["name"],
            "message": [item["status"], str(item["qty"])],
        })
    return entries
```

### Returned entry fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `type` | string | yes | e.g. `"info"`, `"error"` |
| `title` | string | yes | |
| `message` | string or list of strings | no | Defaults to `[]`. Both forms accepted and normalized to `[]string`. |

## Architecture

### `internal/rules/rules.go` changes

- `Rule` struct gains `Script string` (`yaml:"script"`)
- A compiled `starlark.StringDict` (script globals) is stored on the rule after `Load()`
- `Apply()` checks for a compiled script and delegates to `applyScript()` instead of the existing pipeline
- `applyScript()` creates a fresh `starlark.Thread` per call, converts the JSON body to a Starlark dict, calls `process`, and converts the result to `[]Result`

### Load-time flow

```
Load(path) → parse rules.yaml
  └─ for each rule with script:
       read ~/.temporal/scripts/<name>
       starlark.ExecFile(...)  →  StringDict
       store compiled globals on rule
       (any error → log to daemon.log, skip rule)
```

### Per-event flow

```
processItem(rs, body)
  └─ rs.Find(target) → rule
       └─ rule.Apply(body)
            ├─ if script set: applyScript(body) → []Result
            └─ else: existing each/extract pipeline
```

Scripts are reloaded on SIGHUP via the existing `ReloadRules()` mechanism — no new signal handling required.

### Data conversion

**JSON body → Starlark:** raw JSON bytes are unmarshalled into `map[string]interface{}` then recursively converted — objects → `starlark.Dict`, arrays → `starlark.List`, strings/numbers/bools → their Starlark equivalents, `null` → `starlark.None`.

**Starlark result → `[]Result`:** each list element is expected to be a `starlark.Dict`. Fields are extracted by key. Entries and errors are handled per the table below.

## Error Handling

All errors are logged to `daemon.log`. The `/events` endpoint always returns `200` — script errors never surface to the caller.

| Situation | Behaviour |
|---|---|
| Script file not found at load | Log to `daemon.log`, rule skipped |
| Starlark syntax/compile error | Log to `daemon.log`, rule skipped |
| `process` function not defined in script | Log to `daemon.log`, rule skipped |
| Runtime error (script throws) | Log to `daemon.log`, event treated as no-match |
| Bad return type (not a list) | Log to `daemon.log`, event treated as no-match |
| Individual entry missing `type` or `title` | Log to `daemon.log`, skip entry |
| Individual entry has wrong field types | Log to `daemon.log`, skip entry |

## README Update

The `rules.yaml` documentation section must be updated to include:
- `script:` field description with path convention (`~/.temporal/scripts/<name>.star`)
- Note that `script:` is mutually exclusive with `each:` and `extract:`
- A minimal example script showing the `process(body)` function signature and return format

## Dependencies

Add `go.starlark.net` to `go.mod`.
