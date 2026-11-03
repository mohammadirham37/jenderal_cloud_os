# 0008 — Client-rendered SvelteKit console

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

The spec (§5, §9) requires a SvelteKit + Svelte 5 + TypeScript console with Tailwind CSS v4
and xterm.js. All data comes from the Go API, which already enforces authentication,
authorization, CSRF and validation. Should the console render on the server (SSR, server-side
data loading with its own session handling) or in the browser?

## Decision drivers

- One security boundary: the Go API, not a second backend in Node.js.
- No secrets or session handling in the web tier.
- Strict CSP; simple deployment behind Caddy.
- Interactive pages (terminal, live status polling) dominate.

## Considered options

1. SvelteKit with SSR and server `load` functions calling the API with the user's cookie.
2. SvelteKit as a client-rendered app (`ssr = false`) served by `adapter-node`.
3. Static export (`adapter-static`) served directly by Caddy.

## Decision outcome

Chosen option 2. `apps/web/src/routes/+layout.ts` sets `ssr = false`; pages fetch data from
`/api/v1` with the session cookie and the CSRF token (handled by the `api` client in
`apps/web/src/lib/api/client.ts`). The node server (`itk-cloud-web`, `127.0.0.1:3000`) only
serves the shell and assets and applies a nonce-based CSP (`script-src 'self'`,
`frame-ancestors 'none'`). Stack: SvelteKit 3.1, Svelte 5 runes, Vite 8, Tailwind CSS 4,
TypeScript 6, @xterm/xterm 6.

### Consequences

- Good: the web tier holds no credentials and implements no authorization; the API is the
  single enforcement point.
- Good: development uses Vite's proxy for `/api` (incl. WebSocket) — same-origin like
  production.
- Bad: no server-rendered first paint; acceptable for an authenticated console on a LAN.
- Bad: a Node.js runtime is still required on the server (`adapter-node`); option 3 would
  remove it but loses the per-request CSP nonces.

## More information

`apps/web/CONVENTIONS.md`, `apps/web/vite.config.ts`.
