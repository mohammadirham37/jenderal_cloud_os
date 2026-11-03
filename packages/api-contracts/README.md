# Jenderal Cloud OS API contract

`openapi.yaml` is the OpenAPI 3.1 contract of the Jenderal Cloud OS REST API (`/api/v1`) and the
health probes (`/health/live`, `/health/ready`). It documents every route registered in
[`internal/app/api.go`](../../internal/app/api.go).

The Go code is the source of truth. The contract was derived from:

| Contract element | Derived from |
|---|---|
| Paths and methods | the route table in `internal/app/api.go` |
| Request bodies | the handler input structs and their `json` tags (`internal/modules/<module>/*.go`) |
| Response schemas | the response structs (`json` tags) and the `httpx.OK/Created/Accepted/List` envelopes |
| Error responses | `internal/platform/httpx/errors.go` and the `httpx.NewError(...)` calls in handlers |
| Required permissions | the `authz` checks at the top of each handler (also listed in [docs/api.md](../../docs/api.md)) |

## What the contract covers

- **Envelopes**: `{"data": ...}`, paginated `{"data": [...], "meta": {"page", "per_page", "total"}}`
  and errors `{"error": {"code", "message", "details"?, "request_id"?}}`.
- **Security**: `cookieAuth` (session cookie `itk_session`) on every non-public operation;
  unsafe methods additionally declare `csrfToken` (`X-CSRF-Token` header). Public operations
  (`/health/*`, `/api/v1/config`, login, forgot/reset password) declare `security: []`.
- **Async operations** answer `202 Accepted` (workspace create/actions/delete, container
  create/actions, database create, backups, restores).
- **Headers**: `Idempotency-Key` on `POST /workspaces`, `POST /containers`, `POST /databases`;
  `X-Access-Reason` on workspace data endpoints (files, terminals, container create/delete)
  for audited emergency access.
- **Terminal WebSocket** `GET /api/v1/terminals/{id}/ws` documented with a `101` response and
  the frame protocol in the description.
- **App proxy** routes `/api/v1/workspaces/{id}/proxy/{port}/{path}`. Go matches the
  remainder with `{rest...}` (multiple segments); OpenAPI cannot express multi-segment path
  parameters, so `{path}` is documented as "remainder of the URL path".

Field-level quirks are documented as they are, for example `Grants` in the workspace detail
is serialised with capitalised keys (`View`, `Operate`, `Edit`, `Manage`, `Emergency`)
because the Go struct has no `json` tags.

## Validate

```bash
# from the repository root
npx --yes @redocly/cli@latest lint packages/api-contracts/openapi.yaml
```

Last result (Redocly CLI, built-in `recommended` ruleset): **valid, 0 errors, 6 warnings**.
The warnings are expected and intentional:

| Rule | Location | Why it is acceptable |
|---|---|---|
| `no-server-example.com` | `servers[1]` `http://localhost:5173` | The second server is the documented development origin (Vite dev server). |
| `operation-4xx-response` | `/health/live`, `/health/ready`, `/api/v1/config` | These public probes/config endpoints have no client-error responses; readiness reports failures as 503. |
| `operation-2xx-response` | `GET /api/v1/terminals/{id}/ws` | A WebSocket upgrade succeeds with `101 Switching Protocols`, not 2xx. |
| `operation-2xx-response` | `/api/v1/workspaces/{id}/proxy/{port}` | The route only redirects (`302`) to the trailing-slash URL. |

Other useful commands:

```bash
# Render HTML documentation
npx --yes @redocly/cli@latest build-docs packages/api-contracts/openapi.yaml -o /tmp/itk-cloud-api.html

# Bundle into a single resolved file (e.g. for client generators)
npx --yes @redocly/cli@latest bundle packages/api-contracts/openapi.yaml -o /tmp/openapi.bundled.yaml
```

## Keeping it in sync

When a route, request struct or response struct changes:

1. Update the handler and its tests.
2. Update `openapi.yaml` (path, parameters, schema) and the endpoint index in
   [docs/api.md](../../docs/api.md) including the required permission.
3. Run the lint command above; it must report no errors.
4. The web client types (`apps/web/src/lib/api/types*.ts`) are written by hand from the same
   Go structs; update them in the same change.
