# 0007 — Workspace app proxy on a separate origin

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

Users run web applications inside workspaces (JupyterLab, Vite/Laravel dev servers) and need
to open them in the browser. These apps are untrusted code that serves HTML and JavaScript.
If they were served from the console origin, their scripts could call the Jenderal Cloud OS API with
the user's session cookie and read responses (including the CSRF token from `/auth/me`),
fully compromising the account. Container ports must also never be reachable directly from
the LAN.

## Decision drivers

- Browser same-origin isolation between untrusted apps and the console.
- Reuse the existing session for authentication (no second login).
- No direct LAN exposure of container ports.
- Works with Caddy's internal TLS and a single DNS name.

## Considered options

1. Proxy under a path of the console origin.
2. Publish container ports on the LAN.
3. Per-workspace subdomains (wildcard DNS and certificates).
4. A dedicated proxy origin (same host, different port — `APP_PROXY_ORIGIN`, default `:8443`).

## Decision outcome

Chosen option 4 (`internal/modules/appproxy`). Container ports are published on
`127.0.0.1` with random host ports. The API serves `/api/v1/workspaces/{id}/proxy/{port}/…`
only when the request `Host` equals `APP_PROXY_ORIGIN` (config validation requires it to differ
from `APP_BASE_URL`); Caddy's proxy-origin site forwards only those paths. Access requires
owner/editor edit access (no emergency access), a running workspace and a template port. The
proxy strips `itk_session`, `X-CSRF-Token` and `Authorization` from requests and drops
`Set-Cookie: itk_session` from responses. Cookies are not port-scoped, so the console session
authenticates proxy requests, while the different origin isolates app scripts.

### Consequences

- Good: untrusted app scripts cannot read console/API responses; no wildcard DNS/certificates.
- Good: apps stay unreachable from the LAN except through the authenticated proxy.
- Bad: one more port (8443) in the firewall and in the TLS setup.
- Bad: correctness depends on the Caddy route restriction on the proxy origin.
- Bad: apps are shared by all of a workspace's editors under one origin, and apps can set
  their own cookies for the host name; anything on the host that can reach loopback can reach
  app ports without authentication (e.g. tokenless JupyterLab).

## More information

[architecture.md §9.3](../architecture.md#93-app-proxy-on-a-separate-origin), [security.md §6](../security.md#6-known-limitations-and-residual-risks).
