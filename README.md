# Temporal Daemon

A simple daemon for logging events, implemented in Go. It exposes a REST endpoint to receive events and logs them to a file in a structured format. The daemon also manages its own process and logs its activity.

## Features

-   **Event Logging:** Receives events via a POST request and logs them to `~/.temporal/events.log`.
-   **Structured Logging:** Events are logged in a clear, delimited format including timestamp, type, title, and message.
-   **Daemon Control:** `start`, `stop`, and `status` commands to manage the daemon process.
-   **Process Management:** Uses a PID file (`~/.temporal/daemon.pid`) to track the running process.
-   **Daemon Activity Logging:** Logs its own operational messages to `~/.temporal/daemon.log`.
-   **OpenAPI Definition:** Includes a basic OpenAPI (Swagger) definition for the `/events` endpoint in `api/openapi.yaml`.

## Installation

1.  **Clone the repository:**
    ```bash
    git clone <repository_url>
    cd temporal
    ```

2.  **Build and Install:**
    ```bash
    make build
    make install
    ```
    This will compile the `temporal` executable and place it in `~/.local/bin/`. Ensure `~/.local/bin` is in your system's PATH.

## Usage

### Starting the Daemon

To start the `temporal` daemon in the background:

```bash
temporal start
```

### Checking Daemon Status

To check if the daemon is running:

```bash
temporal status
```

### Stopping the Daemon

To stop the running daemon:

```bash
temporal stop
```

### Logging an Event

Once the daemon is running, you can send events to it using a POST request. The server listens on `http://localhost:8005/events`.

Example using `curl`:

```bash
curl -X POST 
  http://localhost:8005/events 
  -H 'Content-Type: application/json' 
  -d '{
    "type": "user_action",
    "title": "User login attempt",
    "message": "User 'john.doe' tried to log in from IP 192.168.1.100"
  }'
```

The event will be logged to `~/.temporal/events.log` in the following format:

```
*****START*****
DATE_TIME TYPE
TITLE
MESSAGE
*****END*****
```

The daemon's own activity (e.g., "Received request on /events") will be logged to `~/.temporal/daemon.log`.

## Development

### Running Tests

```bash
make test
```

### Cleaning Build Artifacts

```bash
make clean
```

## API Definition

The OpenAPI (Swagger) definition for the `/events` endpoint can be found in `api/openapi.yaml`. This file describes the endpoint, expected request body, and responses. You can use tools like [Swagger UI](https://swagger.io/tools/swagger-ui/) to visualize this definition.
