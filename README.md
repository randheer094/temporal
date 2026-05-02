# Temporal

Temporal is a CLI toolkit, written in Go. It currently ships with an event-logging server, accessible under the `server` subcommand. More commands will be added over time.

## Installation

1.  **Clone the repository:**
    ```bash
    git clone <repository_url>
    cd temporal
    ```

2.  **Build and install:**
    ```bash
    make build
    make install
    ```
    Compiles the `temporal` binary and installs it to `~/.local/bin/`. Ensure `~/.local/bin` is on your `PATH`.

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

### Subcommands

| Command | Description |
| --- | --- |
| `temporal server start` | Start the daemon in the background. |
| `temporal server stop` | Stop the running daemon. |
| `temporal server status` | Check whether the daemon is running. |
| `temporal server help` | Show help for the `server` subcommand. |

### Logging an event

The daemon listens on `http://localhost:8005/events`. Send a POST request:

```bash
curl -X POST http://localhost:8005/events \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "user_action",
    "title": "User login attempt",
    "message": "User john.doe tried to log in from IP 192.168.1.100"
  }'
```

Each event is appended to `~/.temporal/events.log` as a single JSON line — see [Logs viewer](#logs-viewer) for the exact schema.

### Rule-based ingestion

The same `POST /events` endpoint accepts arbitrary JSON shapes. The daemon resolves a target (host, path, method, response status) from the body and runs it against `~/.temporal/rules.yaml`. The first matching rule's `extract` templates render the log entry. If no rule matches, the body is decoded as the legacy `{type,title,message}` event. Bodies may also be a JSON **array** — each element is matched and written independently. Writes are queued by the file writer, so the handler returns immediately.

The endpoint **always returns `200 OK`**. The JSON response carries a `status` field of `"ok"` (something was logged), `"no_match"` (nothing logged), or `"ignored"` (request not processable). Empty extractions are skipped — a rule whose templates all render to empty strings produces no log entry.

#### Rule format

```yaml
rules:
  - name: user_login
    match:
      host: api.example.com   # optional; case-insensitive exact
      path: /api/login        # exact, or trailing /* for prefix
      method: POST            # optional; case-insensitive
      status: "2xx"           # optional; "200", "2xx" / "4xx" / "5xx"
    extract:
      type: "user_action"
      title: "Login: {request.body.user.name}"
      message:
        - "status={response.statusCode}"
        - "ip={request.headers.X-Forwarded-For}"
```

A rule with `status` set only matches when the payload includes a response (e.g. a Proxyman `onResponse` forward). Templates use `{gjson.path}` placeholders against the **whole** request body — deep paths, array indexing, and queries are all supported (e.g. `items.0.name`, `users.#(age>18).name`). Missing paths render as empty strings; empty messages are dropped from the array. `rules.yaml` is reloaded on every request, so edits take effect without a restart.

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

Filter cheatsheet (gjson native):

| `each` path                         | Meaning                              |
| ----------------------------------- | ------------------------------------ |
| `items.#(name%"*alert*")#`          | name matches wildcard                |
| `items.#(qty>5)#`                   | numeric comparison                   |
| `items.#(active==true)#`            | boolean                              |
| `items.#(name%"*alert*"&qty>0)#`    | combined (AND)                       |
| `items`                             | every element (no filter)            |

If the path resolves to nothing, no entries are written.

#### Target resolution

The daemon picks `host`, `path`, `method`, and `statusCode` from the body in this priority:

| Field      | Lookup order                                           |
| ---------- | ------------------------------------------------------ |
| host       | parsed from `url` → `host` → `request.host` → `request.url` |
| path       | parsed from `url` → `path` → `request.path` → `request.url` |
| method     | `method` → `request.method`                            |
| statusCode | `status` → `statusCode` → `response.statusCode`        |

`url` may be a full URL (`https://api.example.com/v1/x?ref=foo`) or a bare path. Query strings are stripped before matching.

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
- Optional auto-refresh.
- Per-entry delete (× button).
- "Delete all" / "Delete N matching" — the header button honors both filters.

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
