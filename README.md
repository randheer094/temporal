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

- `~/.temporal/events.log` — received events, in the structured format below. Rotates to `events.log.1` once it exceeds 10MB; the previous backup is overwritten.
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

Events are appended to `~/.temporal/events.log` in this format:

```
*****START*****
DATE_TIME TYPE
TITLE
MESSAGE
*****END*****
```

### Rule-based ingestion

The same `POST /events` endpoint also accepts arbitrary JSON shapes. If the body carries a top-level `url` (and optional `method`) field that matches a rule in `~/.temporal/rules.yaml`, the rule's `extract` templates render the log entry; otherwise the body is decoded as the legacy `{type,title,message}` event.

Bodies may also be a JSON **array** — each element is matched and written independently. Writes are queued by the file writer, so the handler returns immediately.

`rules.yaml`:

```yaml
rules:
  - name: user_login
    match:
      path: /api/login   # matches body.url; supports prefix with trailing /*
      method: POST       # optional; matches body.method (case-insensitive)
    extract:
      type: "user_action"
      title: "Login: {user.name}"
      message: "{event.message} from {meta.ip}"
```

Templates use `{gjson.path}` placeholders against the request body. Deep paths, array indexing, and gjson queries are supported (e.g. `items.0.name`, `users.#(age>18).name`). Missing paths render as empty strings. The file is reloaded on every request, so edits take effect without a restart.

Example request:

```bash
curl -X POST http://localhost:8005/events \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "/api/login",
    "method": "POST",
    "user": { "name": "jane" },
    "event": { "message": "logged in" },
    "meta": { "ip": "1.2.3.4" }
  }'
```

### Logs viewer

The daemon serves a responsive web UI at `http://localhost:8005/logs` for browsing `events.log` (and the rotated backup). Features:

- Pagination (configurable page size).
- Filter by `type`.
- Optional auto-refresh.

The same data is available as JSON at `/logs.json?page=&size=&type=`.

### API definition

OpenAPI (Swagger) definitions live under `docs/` (`swagger.yaml`, `swagger.json`). The running server also serves Swagger UI at `http://localhost:8005/api/docs/`.

## Development

```bash
make test    # run the test suite
make clean   # remove build artifacts
```
