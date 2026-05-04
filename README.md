# Temporal

Temporal is a CLI toolkit, written in Go. It currently ships with an event-logging server, accessible under the `server` subcommand. More commands will be added over time.

## Install

Both paths land the `temporal` binary in `~/.local/bin`. Make sure that directory is on `PATH`.

### From a release (recommended)

Pick the asset for your Mac and curl it straight into `~/.local/bin`:

```bash
mkdir -p ~/.local/bin

# Apple Silicon (arm64)
curl -L -o ~/.local/bin/temporal \
  https://github.com/randheer094/temporal/releases/latest/download/temporal-macos-arm64

# Intel Mac (x86_64)
curl -L -o ~/.local/bin/temporal \
  https://github.com/randheer094/temporal/releases/latest/download/temporal-macos-x86_64

chmod +x ~/.local/bin/temporal
```

### From source

```bash
git clone https://github.com/randheer094/temporal.git
cd temporal
make install        # builds, then moves binary to ~/.local/bin
temporal version
```

## `temporal`

```
temporal [command]
```

| Command | Description |
| --- | --- |
| `temporal server` | Manage the event-logging server (see below). |
| `temporal version` | Print the temporal version. |
| `temporal help` | Show help for any command. |

## `temporal server`

A daemon that exposes a REST endpoint for logging events. It manages its own process lifecycle and writes operational logs of its own.

### Files

- `~/.temporal/events.log` — received events, one JSON object per line: `{"id","timestamp","type","title","message":[...]}`. Rotates to `events.log.1` once it exceeds 10MB; the previous backup is overwritten.
- `~/.temporal/daemon.log` — server activity (e.g. requests received, startup messages).
- `~/.temporal/daemon.pid` — PID of the running daemon.
- `~/.temporal/rules.yaml` — optional. Extraction rules applied to incoming JSON (see [Rule-based ingestion](#rule-based-ingestion)).
- `~/.temporal/scripts/` — optional directory for Python extraction scripts referenced by `script:` fields in `rules.yaml`. Requires `python3` (or `python`) on `PATH`.

### Subcommands

| Command | Description |
| --- | --- |
| `temporal server start` | Start the daemon in the background. |
| `temporal server stop` | Stop the running daemon. |
| `temporal server status` | Check whether the daemon is running. |
| `temporal server rules` | Validate `rules.yaml` and refresh the running daemon. |
| `temporal server help` | Show help for the `server` subcommand. |

### Logging an event

The daemon listens on `http://localhost:8005/events`. POST any JSON body — what gets logged is fully determined by `~/.temporal/rules.yaml` (see [Rule-based ingestion](#rule-based-ingestion)). With no matching rule, the body is dropped.

```bash
curl -X POST http://localhost:8005/events \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "/api/login",
    "method": "POST",
    "user": {"name": "john.doe"},
    "meta": {"ip": "192.168.1.100"}
  }'
```

Logged events are appended to `~/.temporal/events.log` as a single JSON line — see [Logs viewer](#logs-viewer) for the exact schema.

### Rule-based ingestion

`POST /events` accepts arbitrary JSON shapes. The daemon resolves a target (host, path, method, query parameters, response status) from the body and runs it against `~/.temporal/rules.yaml`. The first matching rule's `extract` templates render the log entry. Bodies that don't match any rule are dropped — there is no fallback shape. Bodies may also be a JSON **array**, in which case each element is matched and written independently. Writes are queued by the file writer, so the handler returns immediately.

The endpoint **always returns `200 OK`**. The JSON response carries a `status` field of `"ok"` (something was logged), `"no_match"` (nothing logged), or `"ignored"` (request not processable). Empty extractions are skipped — a rule whose templates all render to empty strings produces no log entry.

#### Rule format

```yaml
rules:
  - name: user_login
    active: true              # optional; default true. Set false to disable a rule without deleting it.
    match:
      host: api.example.com   # optional; case-insensitive exact
      path: /api/login        # exact, or trailing /* for prefix
      method: POST            # optional; case-insensitive
      status: "2xx"           # optional; "200", "2xx" / "4xx" / "5xx"
      query:                  # optional; all listed params must match (extras are ignored)
        ref: homepage         #   require ?ref=homepage
        debug: ""             #   require ?debug present, any value
    extract:
      type: "user_action"
      title: "Login: {request.body.user.name}"
      message:
        - "status={response.statusCode}"
        - "ip={request.headers.X-Forwarded-For}"
```

#### Script-based extraction

Instead of `each:` and `extract:`, a rule can delegate extraction to a Python script:

```yaml
rules:
  - name: my-rule
    match:
      host: api.example.com
      method: POST
    script: script1.py
```

`script:` is a filename resolved relative to `~/.temporal/scripts/`. The daemon invokes `python3` (falling back to `python`) per matching event, so a Python interpreter must be on the daemon's `PATH`. When `script:` is present, `each:` and `extract:` are ignored — the script owns the full extraction pipeline.

The script must define a top-level function named `process` that accepts one argument (the event body as a Python dict) and returns a list of dicts:

```python
# ~/.temporal/scripts/script1.py
import json

def process(body):
    # Proxyman forwards response bodies as raw strings; parse if needed.
    raw = body.get("response", {}).get("body", {})
    if isinstance(raw, str):
        raw = json.loads(raw)

    entries = []
    for item in raw.get("items", []):
        entries.append({
            "type": "info",
            "title": item["name"],
            "message": [item["status"]],
        })
    return entries
```

Each returned dict must have `type` and `title` (strings). `message` is optional — either a string or a list of strings. Entries missing required fields are skipped and logged to `daemon.log`.

Scripts run in an unsandboxed `python3` subprocess with the full standard library available (`json`, `re`, `datetime`, etc.). There's no network or filesystem isolation — only run scripts you trust. Each invocation is capped at 5 seconds wall-clock; longer runs are killed and logged.

Script errors (missing file, syntax error, runtime exception, bad return type, timeout) are all logged to `daemon.log`; the `/events` endpoint always returns `200`. Scripts are reloaded with `temporal server rules` — the same SIGHUP mechanism used for YAML rules.

A rule with `status` set only matches when the payload includes a response (e.g. a Proxyman `onResponse` forward). Templates use `{gjson.path}` placeholders against the **whole** request body — deep paths, array indexing, and queries are all supported (e.g. `items.0.name`, `users.#(age>18).name`). Missing paths render as empty strings; empty messages are dropped from the array.

`rules.yaml` is loaded once at daemon startup and cached. After editing the file, run `temporal server rules` — the command parses it locally first (so you see syntax errors immediately) and, if valid, sends the daemon SIGHUP to swap in the new rule set. A broken file never replaces a working one.

#### Match operators

Each field inside a `match` block is a separate test; **all** present fields must pass for that block to match. Omitted fields are wildcards.

Top-level `active: false` on a rule removes it from matching entirely (the daemon skips it before checking any field). The default when `active` is omitted is `true`.

| Field    | Type   | Semantic                                                                                          |
| -------- | ------ | ------------------------------------------------------------------------------------------------- |
| `host`   | string | Case-insensitive exact comparison against the resolved host. Omit to allow any host.              |
| `path`   | string | Exact match. Trailing `/*` makes it a prefix match (e.g. `/api/*` matches `/api/x` and `/api/x/y` but not `/apix`). Omit to allow any path. |
| `method` | string | Case-insensitive exact comparison against the HTTP method. Omit to allow any method.              |
| `status` | string | Either an exact 3-digit code (`"201"`) or an `Nxx` family (`"2xx"`/`"4xx"`/`"5xx"`). When set, the rule only matches payloads that carry a response status (request-only forwards are skipped). Omit to ignore status. |
| `query`  | map    | Map of `param: value` pairs. Every listed param must be present in the request URL; an empty value (`""`) means "param must be present, value any". Only the params listed in the rule are checked — extra params in the request are ignored, so `?q1=v1&q2=v2` still matches a rule that only requires `q1: v1`. Omit to ignore query params. |

#### Multiple match / each / extract

`match`, `each`, and `extract` each accept a single value **or** a list. The semantics:

- **Multiple `match` blocks** — OR semantics. The rule fires if **any** block matches.
- **Multiple `each` paths** — the resolved elements from every path are unioned into a single stream of context bodies (in the order the paths are listed).
- **Multiple `extract` blocks** — each block renders against every context body and produces its own log entry. Empty entries are still dropped.

If you combine all three, the entry count is `len(context bodies) × len(extracts)`. With no `each` the body itself is the single context.

```yaml
- name: orders_and_alerts
  match:
    - { host: api.example.com, path: /api/cart }
    - { host: api.example.com, path: /api/order }
  each:
    - 'request.body.items.#(qty>0)#'
    - 'request.body.alerts'
  extract:
    - { type: "cart_item", title: "{name}", message: "qty {qty}" }
    - { type: "cart_audit", title: "{name}", message: "id {id}" }
```

`extract.message` accepts either a single string or a list of strings (rendered as separate lines in the log entry).

#### Fan-out across nested arrays (`each`)

When a payload contains a list and you want **one log entry per matching item**, set `each` to a [gjson path](https://github.com/tidwall/gjson#path-syntax) that resolves to those elements. Each matched element becomes the rendering context, so templates reference fields directly (`{name}`, not `{items.0.name}`). gjson queries inside the path provide the filter:

```yaml
- name: cart_alerts
  match: { path: /api/cart }
  each: 'items.#(name%"*alert*")#'   # only items whose name contains "alert"
  extract:
    type: "cart_alert"
    title: "{name}"
    message:
      - "qty {qty}"
      - "id {id}"
```

If the path resolves to nothing, no entries are written.

#### gjson operator reference

Both template placeholders (`"{path}"`) and `each` paths use [gjson syntax](https://github.com/tidwall/gjson#path-syntax). The operators that come up most often:

| Operator        | Where               | Meaning                                                                                          |
| --------------- | ------------------- | ------------------------------------------------------------------------------------------------ |
| `.`             | path                | Field separator. `request.body.user.name` walks four levels.                                     |
| `0`, `1`, …     | path                | Array index. `items.0.name` reads the first item's name.                                          |
| `#`             | path                | Length / "every element" marker. `items.#` is the count; `items.#.name` is each item's name.     |
| `#(...)`        | path query          | First element matching the predicate.                                                            |
| `#(...)#`       | path query          | All elements matching the predicate (returns a JSON array — required for `each` fan-out).        |
| `==` / `!=`     | predicate           | Equality. `items.#(active==true)#`, `items.#(qty!=0)#`.                                          |
| `<` / `<=` / `>` / `>=` | predicate   | Numeric comparison. `items.#(qty>5)#`.                                                           |
| `%`             | predicate           | Wildcard pattern match (gjson "LIKE"). `*` matches any run of chars, `?` matches one. `name%"*alert*"`. Without wildcards behaves as exact match. |
| `!%`            | predicate           | Inverse wildcard match.                                                                          |
| `@this`         | path                | The whole document at this scope (gjson root selector).                                          |

> **Compound predicates aren't supported.** gjson's `#(a&b)` / `#(a|b)` syntax is unreliable in practice — combinators may silently match nothing, or match everything. To express AND, set `each` to one filter and discard non-matches in the templates (or use two separate rules). To express OR, list multiple `each` paths (the union becomes the rule's context body stream).

**Quoting gotcha:** YAML predicates like `%"foo"` contain literal double quotes. Always wrap the whole `each` value in **single** quotes, otherwise YAML will mis-parse the inner `"` and the entire rules file fails to load (every event then drops as `no_match`):

```yaml
each: 'items.#(name%"*alert*")#'   # ✓
each: "items.#(name%\"*alert*\")#" # also works but ugly
each: "items.#(name%"*alert*")#"   # ✗ YAML parse error
```

Empty rendered fields are dropped: an extract whose `type`, `title`, and every `message` line all render to empty strings produces no log entry.

#### Target resolution

The daemon picks `host`, `path`, `method`, `query`, and `statusCode` from the body in this priority:

| Field      | Lookup order                                           |
| ---------- | ------------------------------------------------------ |
| host       | parsed from `url` → `host` → `request.host` → `request.url` |
| path       | parsed from `url` → `path` → `request.path` → `request.url` |
| method     | `method` → `request.method`                            |
| query      | parsed from `url` → `path` → `request.path` → `request.url` (first source carrying a query string wins) |
| statusCode | `status` → `statusCode` → `response.statusCode`        |

`url` may be a full URL (`https://api.example.com/v1/x?ref=foo`) or a bare path. Query strings are parsed into the target so `match.query` can constrain on them; the path itself is matched without the query.

#### Proxyman integration

Drop [`examples/proxyman-forward.js`](examples/proxyman-forward.js) into a Proxyman script (Tools → Scripting). It forwards every intercepted request and/or response to `http://localhost:8005/events` using `$http.post` (macOS) or `axios.post` (Windows/Linux). Each forward looks like:

```json
{
  "url": "https://api.example.com/api/login?ref=abc",
  "method": "POST",
  "request":  { "host": "api.example.com", "path": "/api/login", "method": "POST",
                "headers": {...}, "body": {...} },
  "response": { "statusCode": 200, "headers": {...}, "body": {...} }
}
```

Both `onRequest` and `onResponse` hooks are supported — for request-only forwards, just omit the `response` block (or use a rule without a `status` constraint). See [`examples/rules.yaml`](examples/rules.yaml) for a working starting point.

#### Direct curl example

```bash
curl -X POST http://localhost:8005/events \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://api.example.com/api/login",
    "method": "POST",
    "request":  { "body": { "user": { "name": "jane" } } },
    "response": { "statusCode": 200, "body": { "ok": true } }
  }'
```

### Logs viewer

The daemon serves a responsive web UI at `http://localhost:8005/logs` for browsing `events.log` (and the rotated backup). Features:

- Pagination (configurable page size).
- Filter by `type` (dropdown).
- Title search (case-insensitive substring).
- Optional auto-refresh (5s; entries are diffed across refreshes so the list doesn't flicker).
- Per-entry delete (× button).
- "Delete all" / "Delete N matching" — the header button honors both filters.
- CSV export of the current filter (`type` and `q`) via the **Export CSV** button or `/logs.csv?type=&q=`. Entries with multiple message lines produce one row per line, with `timestamp`, `type`, and `title` repeated.

The same data is available as JSON at `/logs.json?page=&size=&type=&q=`.

Storage format is **JSON lines** — one event per line, e.g.:

```
{"id":"a1b2c3d4","timestamp":"2025-05-02T10:30:01.234Z","type":"user_action","title":"Login OK: jane","message":["ip=1.2.3.4","ua=curl/8"]}
```

The `id` is a random 8-hex-char identifier assigned at write time, used for individual deletion.

#### Delete API

| Method   | Path             | Description                                                                 |
| -------- | ---------------- | --------------------------------------------------------------------------- |
| `DELETE` | `/logs`          | Delete all entries. With `?type=X` and/or `?q=text`, only matching entries. |
| `DELETE` | `/logs/{id}`     | Delete a single entry by id. 404 if not found.                              |

Examples:

```bash
# Wipe everything
curl -X DELETE http://localhost:8005/logs

# Delete all "error" type entries whose title contains "timeout"
curl -X DELETE 'http://localhost:8005/logs?type=error&q=timeout'

# Delete a single entry
curl -X DELETE http://localhost:8005/logs/a1b2c3d4
```

Deletion pauses the writer briefly, drains pending writes, rewrites both `events.log` and `events.log.1` atomically, then resumes. Concurrent POSTs are not lost.

### API definition

OpenAPI (Swagger) definitions live under `docs/` (`swagger.yaml`, `swagger.json`). The running server also serves Swagger UI at `http://localhost:8005/api/docs/`.

## Development

```bash
make test    # run the test suite
make clean   # remove build artifacts
```
