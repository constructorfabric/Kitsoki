# Add Basic Health Check

A small epic to add a health-check endpoint to the API. Three independent slices:

## Slice 1 — health model
Add a `HealthStatus` struct with `Status string` and `Version string` fields
under `internal/health/`. No HTTP layer yet — just the model and its unit tests.

Gate: `go test ./internal/health/...`

## Slice 2 — health handler
Add a `GET /health` handler that serialises `HealthStatus` to JSON
(`{"status":"ok","version":"..."}`) in `internal/handler/`.
Depends on slice 1 (imports the health model).

Gate: `go test ./internal/handler/...`

## Slice 3 — wire health route
Register the health handler in the router (`internal/router/`).
Add a smoke-test that calls `/health` and asserts a 200 response.
Depends on slice 2 (imports the handler).

Gate: `go test ./internal/router/...`
