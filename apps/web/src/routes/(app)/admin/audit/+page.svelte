<script lang="ts">
	import { ChevronDown, ChevronRight, Download, ScrollText, X } from '@lucide/svelte';
	import { page } from '$app/state';
	import {
		Badge,
		Button,
		Card,
		CopyButton,
		DataTable,
		EmptyState,
		PageHeader,
		Pagination,
		SearchInput
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { AuditLog } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';

	const perPage = 50;
	const allowed = $derived(auth.can('audit.view'));

	let q = $state('');
	let search = $state('');
	let actionInput = $state('');
	let action = $state('');
	let outcome = $state('');
	let resourceType = $state('');
	let resourceTypeInput = $state('');
	let fromDate = $state('');
	let toDate = $state('');
	let actorId = $state(page.url.searchParams.get('actor_id') ?? '');
	let sort = $state('-occurred_at');
	let pageNo = $state(1);
	let expanded = $state<Record<number, boolean>>({});

	/** yyyy-mm-dd (local) → RFC 3339 at the start or end of that local day. */
	function dayBound(d: string, end: boolean): string | undefined {
		if (!d) return undefined;
		const t = new Date(`${d}T${end ? '23:59:59.999' : '00:00:00'}`);
		return Number.isNaN(t.getTime()) ? undefined : t.toISOString();
	}

	const logs = resource(
		guarded(['audit.view'], () =>
			api.list<AuditLog>('/admin/audit-logs', {
				q: search,
				action,
				outcome,
				resource_type: resourceType,
				actor_id: actorId,
				from: dayBound(fromDate, false),
				to: dayBound(toDate, true),
				sort,
				page: pageNo,
				per_page: perPage
			})
		)
	);

	function resetPage() {
		pageNo = 1;
		expanded = {};
	}
	function applyText(e: SubmitEvent) {
		e.preventDefault();
		action = actionInput.trim();
		resourceType = resourceTypeInput.trim();
		resetPage();
	}
	const hasFilters = $derived(
		!!(search || action || outcome || resourceType || fromDate || toDate || actorId)
	);
	function clearFilters() {
		q = search = action = actionInput = outcome = resourceType = resourceTypeInput = '';
		fromDate = toDate = actorId = '';
		resetPage();
	}

	const outcomeTone = { success: 'success', failure: 'danger', denied: 'warning' } as const;

	function csvCell(v: unknown): string {
		const s = v === null || v === undefined ? '' : typeof v === 'string' ? v : JSON.stringify(v);
		return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
	}
	function exportCsv() {
		const rows = logs.data?.data ?? [];
		if (rows.length === 0) return;
		const header = [
			'id',
			'occurred_at',
			'actor_id',
			'actor_username',
			'action',
			'resource_type',
			'resource_id',
			'outcome',
			'reason',
			'ip',
			'user_agent',
			'request_id',
			'metadata'
		] as const;
		const lines = [
			header.join(','),
			...rows.map((r) => header.map((h) => csvCell(r[h])).join(','))
		];
		const blob = new Blob([lines.join('\r\n')], { type: 'text/csv;charset=utf-8' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = `audit-log-page-${pageNo}-${new Date().toISOString().slice(0, 19).replace(/:/g, '')}.csv`;
		a.click();
		URL.revokeObjectURL(url);
		toast.success('CSV exported', `${rows.length} entries from the current page.`);
	}

	const columns = [
		{ key: 'expand', label: '', class: 'w-8' },
		{ key: 'occurred_at', label: 'Time', sortable: true },
		{ key: 'actor', label: 'Actor' },
		{ key: 'action', label: 'Action', sortable: true },
		{ key: 'resource', label: 'Resource' },
		{ key: 'outcome', label: 'Outcome' },
		{ key: 'ip', label: 'IP' }
	];
</script>

<PageHeader
	title="Audit Logs"
	description="Every security-relevant action: who did what, to which resource, from where and with what result."
>
	{#snippet actions()}
		{#if allowed}
			<Button onclick={exportCsv} disabled={!logs.data?.data.length}
				><Download class="size-4" /> Export page as CSV</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	<Card padded={false}>
		<form
			class="grid gap-3 border-b border-border px-5 py-4 sm:grid-cols-2 lg:grid-cols-4"
			onsubmit={applyText}
		>
			<div>
				<span class="label">Search</span>
				<SearchInput
					bind:value={q}
					placeholder="Action, username or resource ID"
					onsearch={(v) => {
						search = v.trim();
						resetPage();
					}}
				/>
			</div>
			<div>
				<label for="f-action" class="label">Action</label>
				<input
					id="f-action"
					class="input font-mono"
					placeholder="e.g. user.* or workspace.create"
					bind:value={actionInput}
					onblur={() => {
						if (actionInput.trim() !== action) {
							action = actionInput.trim();
							resetPage();
						}
					}}
				/>
			</div>
			<div>
				<label for="f-rtype" class="label">Resource type</label>
				<input
					id="f-rtype"
					class="input"
					placeholder="e.g. workspace, user"
					bind:value={resourceTypeInput}
					onblur={() => {
						if (resourceTypeInput.trim() !== resourceType) {
							resourceType = resourceTypeInput.trim();
							resetPage();
						}
					}}
				/>
			</div>
			<div>
				<label for="f-outcome" class="label">Outcome</label>
				<select id="f-outcome" class="input" bind:value={outcome} onchange={resetPage}>
					<option value="">Any outcome</option>
					<option value="success">Success</option>
					<option value="failure">Failure</option>
					<option value="denied">Denied</option>
				</select>
			</div>
			<div>
				<label for="f-from" class="label">From (local date)</label>
				<input id="f-from" type="date" class="input" bind:value={fromDate} onchange={resetPage} />
			</div>
			<div>
				<label for="f-to" class="label">To (local date)</label>
				<input id="f-to" type="date" class="input" bind:value={toDate} onchange={resetPage} />
			</div>
			<div class="flex items-end gap-2 lg:col-span-2">
				<Button type="submit">Apply</Button>
				{#if hasFilters}<Button variant="ghost" onclick={clearFilters}>Clear filters</Button>{/if}
				{#if actorId}
					<span
						class="ml-auto inline-flex items-center gap-1 rounded-full bg-accent-soft px-2.5 py-1 text-xs text-accent"
					>
						Actor <span class="font-mono">{actorId.slice(0, 8)}</span>
						<button
							type="button"
							aria-label="Remove actor filter"
							onclick={() => {
								actorId = '';
								resetPage();
							}}><X class="size-3" /></button
						>
					</span>
				{/if}
			</div>
			<p class="text-xs text-fg-muted sm:col-span-2 lg:col-span-4">
				Use <span class="kbd">*</span> as a wildcard in the action filter. Press Enter or Apply to filter.
			</p>
		</form>

		<DataTable
			{columns}
			rows={logs.data?.data ?? []}
			loading={logs.loading}
			error={logs.error}
			retry={logs.reload}
			bind:sort
			onsort={resetPage}
		>
			{#snippet empty()}
				<EmptyState
					icon={ScrollText}
					title={hasFilters ? 'No entries match the filters' : 'No audit entries yet'}
					description={hasFilters ? 'Widen the date range or clear the filters.' : undefined}
				>
					{#snippet action()}{#if hasFilters}<Button onclick={clearFilters}>Clear filters</Button
							>{/if}{/snippet}
				</EmptyState>
			{/snippet}
			{#snippet row(l)}
				{@const open = !!expanded[l.id]}
				<tr class="hover:bg-panel-2/40">
					<td class="py-2.5 pl-4">
						<button
							type="button"
							class="rounded p-0.5 text-fg-muted hover:bg-panel-2 hover:text-fg"
							aria-expanded={open}
							aria-controls="audit-{l.id}"
							aria-label="{open ? 'Hide' : 'Show'} details"
							onclick={() => (expanded[l.id] = !open)}
						>
							{#if open}<ChevronDown class="size-4" />{:else}<ChevronRight class="size-4" />{/if}
						</button>
					</td>
					<td class="px-4 py-2.5 text-xs whitespace-nowrap tabular-nums"
						>{dateTime(l.occurred_at)}</td
					>
					<td class="px-4 py-2.5">
						{#if l.actor_id}
							<a href="/admin/users/{l.actor_id}" class="hover:underline"
								>{l.actor_username ?? l.actor_id.slice(0, 8)}</a
							>
						{:else}<span class="text-fg-muted">{l.actor_username ?? 'system'}</span>{/if}
					</td>
					<td class="px-4 py-2.5 font-mono text-xs">{l.action}</td>
					<td class="px-4 py-2.5 text-xs">
						{l.resource_type || '—'}
						{#if l.resource_id}<span class="font-mono text-fg-muted" title={l.resource_id}
								>{l.resource_id.length > 12 ? `${l.resource_id.slice(0, 8)}…` : l.resource_id}</span
							>{/if}
					</td>
					<td class="px-4 py-2.5"><Badge tone={outcomeTone[l.outcome]} dot>{l.outcome}</Badge></td>
					<td class="px-4 py-2.5 font-mono text-xs text-fg-muted">{l.ip ?? '—'}</td>
				</tr>
				{#if open}
					<tr id="audit-{l.id}" class="bg-panel-2/40">
						<td></td>
						<td colspan="6" class="px-4 py-3">
							<dl class="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-[auto_1fr]">
								<dt class="text-fg-muted">Reason</dt>
								<dd class="whitespace-pre-line">{l.reason || '—'}</dd>
								<dt class="text-fg-muted">Request ID</dt>
								<dd class="flex items-center gap-1 font-mono">
									{l.request_id ?? '—'}{#if l.request_id}<CopyButton
											text={l.request_id}
											label="Copy request ID"
										/>{/if}
								</dd>
								<dt class="text-fg-muted">Resource ID</dt>
								<dd class="font-mono break-all">{l.resource_id || '—'}</dd>
								<dt class="text-fg-muted">User agent</dt>
								<dd class="break-all">{l.user_agent ?? '—'}</dd>
								<dt class="text-fg-muted">Metadata</dt>
								<dd>
									{#if l.metadata && Object.keys(l.metadata).length}
										<pre
											class="max-h-72 overflow-auto rounded-md border border-border bg-panel p-3 font-mono text-xs">{JSON.stringify(
												l.metadata,
												null,
												2
											)}</pre>
									{:else}—{/if}
								</dd>
							</dl>
						</td>
					</tr>
				{/if}
			{/snippet}
		</DataTable>
		{#if logs.data && logs.data.meta.total > 0}
			<div class="border-t border-border px-4">
				<Pagination
					bind:page={pageNo}
					{perPage}
					total={logs.data.meta.total}
					onchange={() => (expanded = {})}
				/>
			</div>
		{/if}
	</Card>
{/if}
