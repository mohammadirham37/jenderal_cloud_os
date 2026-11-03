# ITK Cloud web — conventions

Read this before adding pages. The foundation (API client, stores, UI
components, layout, auth pages, dashboard) already exists; build on it.

## Stack

- SvelteKit **3.1** (config lives in `vite.config.ts`, no `svelte.config.js`),
  Svelte **5 runes** only (`$state`, `$derived`, `$effect`, `$props`, snippets,
  `{#snippet}` / `{@render}`; no `export let`, no `on:click`, no stores from
  `svelte/store`).
- Client-rendered app (`ssr = false` in `src/routes/+layout.ts`). Pages fetch
  data in the component (no `+page.ts` load functions needed).
- Tailwind CSS v4 with design tokens defined in `src/routes/layout.css`
  (`bg-panel`, `bg-panel-2`, `bg-surface`, `text-fg`, `text-fg-muted`,
  `text-fg-subtle`, `border-border`, `border-border-strong`, `bg-accent`,
  `text-accent`, `bg-accent-soft`, `text-danger`, `bg-danger-soft`, `text-success`,
  `bg-success-soft`, `text-warning`, `bg-warning-soft`, `text-info`, `bg-info-soft`)
  and utility classes `.input`, `.label`, `.card`, `.link`, `.kbd`. Never use raw
  colours like `text-gray-500`; both light and dark themes must work.
- Icons: `@lucide/svelte` (e.g. `import { Play } from '@lucide/svelte'`).

## Imports — always with explicit extensions (SvelteKit 3 `#lib`)

```ts
import { api, ApiError, errorMessage, idempotencyKey } from '#lib/api/client.ts';
import type { Workspace } from '#lib/api/types.ts';
import { resource } from '#lib/api/resource.svelte.ts';
import { Button, Card, DataTable, PageHeader, StatusBadge } from '#lib/components/ui/index.ts';
import { auth } from '#lib/stores/auth.svelte.ts';
import { toast } from '#lib/stores/toast.svelte.ts';
import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
import { appConfig } from '#lib/stores/config.svelte.ts';
import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
import { bytes, cores, dateTime, relative, percent, GiB, MiB, titleCase } from '#lib/format.ts';
import { page } from '$app/state';
import { goto } from '$app/navigation';
```

## API client

- `api.get<T>(path, query?)` returns `data` of the `{"data": ...}` envelope.
- `api.list<T>(path, query?)` returns `{ data: T[], meta: { page, per_page, total } }`
  for paginated endpoints (`?page=&per_page=&sort=&q=`; sort `-field` = desc).
- `api.post/put/patch<T>(path, body?, opts?)`, `api.del(path, query?)`.
- `opts.headers` for extra headers (e.g. `{'Idempotency-Key': idempotencyKey()}`,
  `{'X-Access-Reason': reason}`).
- Errors are `ApiError` with `.status`, `.code`, `.message`, `.details`
  (field errors for HTTP 422). Show `.details[field]` under the matching form
  field and `.message` otherwise. CSRF is handled automatically.
- 401 / MFA / forced password change redirects are handled globally.
- Paths are relative to `/api/v1` (e.g. `api.get('/workspaces/' + id)`).
- The Go handlers are the source of truth for request/response shapes:
  `internal/modules/<module>/*.go` (structs with `json:"..."` tags) and the
  route table in `internal/app/api.go`. Read them; do not guess fields.

## Page checklist (Definition of Done)

Every page must have:

1. `<PageHeader title=... description=...>` (sets `<title>`), actions in the
   `actions` snippet.
2. **Loading** (Skeleton / Spinner), **empty** (EmptyState with guidance and a
   call to action when the user can act), **error** (ErrorState with retry),
   and **success** feedback (`toast.success(...)`).
3. Permission-aware UI: hide/disable actions with `auth.can('perm')`, but
   remember the backend enforces everything — handle 403 gracefully.
4. Destructive actions (delete, stop, restart, suspend, release, restore,
   revoke…) ask for confirmation via
   `await confirmDialog.confirm({ title, message, danger: true, confirmLabel })`
   or `confirmDialog.ask({ ..., reasonLabel, reasonRequired, requireText })`.
5. Forms: `<Field label id error hint>` + `.input`; disable submit while
   submitting (`<Button loading>`); map 422 `details` to fields.
6. Never invent data. When a value is null/unavailable show "—" or
   "Unavailable" and explain why if possible. Never show fake metrics.
7. Accessible: labels for inputs, `aria-*` on custom widgets, keyboard
   reachable, focus visible, sufficient contrast, tables with `<th scope>`.
8. Detail pages register a breadcrumb label: `crumbs.set(id, name)`.
9. Live status (e.g. provisioning) polls with `resource(fn, { poll: 3000 })`
   only while something is pending; otherwise poll slowly (≥ 15 s) or not at all.
10. UI copy in clear English; times shown in local time via `dateTime`/`relative`.

## Components (`#lib/components/ui/index.ts`)

`Alert(tone,title)`, `Badge(tone,dot)`, `Button(variant: primary|secondary|danger|ghost|subtle, size: sm|md, loading, href)`,
`Card(title, description, actions snippet, padded)`, `ConfirmDialog` (global),
`CopyButton(text,label)`, `DataTable(columns,rows,loading,error,retry,sort,onsort,row snippet,empty snippet,emptyTitle,emptyDescription)`
— rows are rendered by the caller as `<tr>`; `EmptyState(title,description,icon,action snippet)`,
`ErrorState(error,retry)`, `Field(label,id,error,hint,required)`, `LineChart(times,series[{label,values,color}],format,yMax,title,height)`,
`Meter(value,max,label,display)`, `Modal(open bindable,title,description,size,footer snippet,onclose)`,
`PageHeader`, `Pagination(page bindable,perPage,total,onchange)`, `SearchInput(value bindable,placeholder,onsearch)`,
`Skeleton(lines)`, `Spinner(size)`, `StatCard(label,value,sub,icon)`, `StatusBadge(status,pending)`,
`Switch(checked bindable,label,id,onchange)`, `Tabs(tabs[{id,label,count}],active bindable,onchange)`.

Chart colours: use CSS variables, e.g. `'var(--color-accent)'`, `'var(--color-info)'`,
`'var(--color-warning)'`, `'var(--color-danger)'`, `'var(--color-success)'`.

Area-specific components go in `src/lib/components/<area>/` (e.g.
`src/lib/components/workspaces/WorkspaceActions.svelte`).

## Ownership rules for parallel work

- Only create/edit files inside the areas assigned to you.
- Do **not** edit shared foundation files (`src/lib/api/*`, `src/lib/stores/*`,
  `src/lib/components/ui/*`, `src/lib/components/layout/*`, `src/routes/+layout*`,
  `src/routes/(app)/+layout.svelte`, `layout.css`, `package.json`). If you need
  extra types, put them in `src/lib/api/types-<area>.ts`. If a shared change is
  truly required, describe it in your final report instead of making it.
- Do not run `pnpm dev`/`vite` servers or install packages.

## Verification

From `apps/web`:

```bash
pnpm exec prettier --write <your files>
pnpm exec eslint <your files>
pnpm run check          # svelte-check: must report 0 errors and 0 warnings
```

Unit tests (optional, for pure logic): `src/**/*.spec.ts` run with
`pnpm exec vitest run --project server`.
