// Proxyman scripting snippet: forward intercepted traffic to the Temporal
// daemon at http://localhost:8005/events.
//
// Drop this into a Proxyman script (Tools -> Scripting). Both hooks are
// optional — keep onRequest only, onResponse only, or both depending on
// what you want to log. The Temporal daemon matches each forward against
// rules.yaml and writes one log entry per match.
//
// macOS uses the built-in $http; Windows/Linux uses axios. The snippet
// below auto-detects which is available.

const TEMPORAL_URL = "http://localhost:8005/events";

async function forward(payload) {
  const body = JSON.stringify(payload);
  const headers = { "Content-Type": "application/json" };
  if (typeof $http !== "undefined") {
    await $http.post(TEMPORAL_URL, { headers, body });
  } else if (typeof axios !== "undefined") {
    await axios.post(TEMPORAL_URL, payload, { headers });
  }
}

async function onRequest(context, url, request) {
  await forward({
    url: url,
    method: request.method,
    request: {
      host: request.host,
      path: request.path,
      method: request.method,
      headers: request.headers,
      queries: request.queries,
      body: request.body,
    },
  });
  return request;
}

async function onResponse(context, url, request, response) {
  await forward({
    url: url,
    method: request.method,
    request: {
      host: request.host,
      path: request.path,
      method: request.method,
      headers: request.headers,
      queries: request.queries,
      body: request.body,
    },
    response: {
      statusCode: response.statusCode,
      headers: response.headers,
      body: response.body,
    },
  });
  return response;
}
