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

- `~/.temporal/events.log` — received events, in the structured format below.
- `~/.temporal/daemon.log` — server activity (e.g. requests received, startup messages).
- `~/.temporal/daemon.pid` — PID of the running daemon.

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

### API definition

OpenAPI (Swagger) definitions live under `docs/` (`swagger.yaml`, `swagger.json`). The running server also serves Swagger UI at `http://localhost:8005/api/docs/`.

## Development

```bash
make test    # run the test suite
make clean   # remove build artifacts
```
