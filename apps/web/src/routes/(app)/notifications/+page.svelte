<script lang="ts">
	import {
		Bell,
		CheckCheck,
		CircleAlert,
		CircleCheck,
		ExternalLink,
		Info,
		TriangleAlert
	} from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import {
		Button,
		Card,
		EmptyState,
		ErrorState,
		PageHeader,
		Pagination,
		Skeleton,
		Switch
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Notification } from '#lib/api/types.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { notificationCounter } from '#lib/stores/notifications.svelte.ts';
	import { dateTime, relative, titleCase } from '#lib/format.ts';

	const perPage = 25;
	let pageNo = $state(1);
	let unreadOnly = $state(false);
	let type = $state('');

	const list = resource(
		() =>
			api.list<Notification>('/notifications', {
				page: pageNo,
				per_page: perPage,
				unread: unreadOnly ? 'true' : undefined,
				type
			}),
		{ poll: 30000 }
	);
	const items = $derived(list.data?.data ?? []);
	const unread = $derived(Number(list.data?.meta.unread ?? 0));
	const total = $derived(list.data?.meta.total ?? 0);

	const types = [
		'system',
		'workspace',
		'gpu',
		'quota',
		'maintenance',
		'security',
		'backup',
		'alert'
	];
	const severity = {
		info: { icon: Info, cls: 'bg-info-soft text-info', label: 'Information' },
		success: { icon: CircleCheck, cls: 'bg-success-soft text-success', label: 'Success' },
		warning: { icon: TriangleAlert, cls: 'bg-warning-soft text-warning', label: 'Warning' },
		critical: { icon: CircleAlert, cls: 'bg-danger-soft text-danger', label: 'Critical' }
	};

	let markingAll = $state(false);

	async function open(n: Notification) {
		if (!n.read_at) {
			try {
				await api.post(`/notifications/${n.id}/read`);
				n.read_at = new Date().toISOString();
				notificationCounter.refresh();
				if (!n.link) list.reload();
			} catch (err) {
				toast.error('Could not mark as read', errorMessage(err));
			}
		}
		if (n.link) {
			if (/^https?:\/\//.test(n.link)) window.open(n.link, '_blank', 'noopener');
			else await goto(n.link);
		}
	}

	async function markAll() {
		markingAll = true;
		try {
			await api.post('/notifications/read-all');
			toast.success('All notifications marked as read');
			notificationCounter.refresh();
			await list.reload();
		} catch (err) {
			toast.error('Could not mark notifications as read', errorMessage(err));
		} finally {
			markingAll = false;
		}
	}
</script>

<PageHeader
	title="Notifications"
	description={list.data
		? unread
			? `${unread} unread notification${unread === 1 ? '' : 's'}`
			: 'You are all caught up.'
		: 'Updates about your workspaces, GPU requests, quotas and the platform.'}
>
	{#snippet actions()}
		<Button onclick={markAll} loading={markingAll} disabled={unread === 0}
			><CheckCheck class="size-4" /> Mark all read</Button
		>
	{/snippet}
</PageHeader>

<Card padded={false}>
	<div class="flex flex-wrap items-center gap-4 border-b border-border px-4 py-3">
		<div class="flex items-center gap-2">
			<Switch
				id="notif-unread"
				label="Show unread notifications only"
				bind:checked={unreadOnly}
				onchange={() => (pageNo = 1)}
			/>
			<label for="notif-unread" class="text-sm text-fg-muted">Unread only</label>
		</div>
		<label class="sr-only" for="notif-type">Type</label>
		<select id="notif-type" class="input w-auto" bind:value={type} onchange={() => (pageNo = 1)}>
			<option value="">All types</option>
			{#each types as t (t)}<option value={t}>{t === 'gpu' ? 'GPU' : titleCase(t)}</option>{/each}
		</select>
	</div>

	{#if list.error}
		<ErrorState error={list.error} retry={list.reload} />
	{:else if !list.data}
		<div class="p-5"><Skeleton lines={6} /></div>
	{:else if items.length === 0}
		<EmptyState
			icon={Bell}
			title={unreadOnly || type ? 'No matching notifications' : 'No notifications'}
			description={unreadOnly || type
				? 'Clear the filters to see older or read notifications.'
				: 'You will be notified here about workspace events, GPU decisions, quota warnings and announcements.'}
		/>
	{:else}
		<ul class="divide-y divide-border" aria-busy={list.loading}>
			{#each items as n (n.id)}
				{@const s = severity[n.severity] ?? severity.info}
				<li>
					<button
						type="button"
						class="flex w-full items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-panel-2/60 focus-visible:bg-panel-2/60 {n.read_at
							? ''
							: 'bg-accent-soft/40'}"
						onclick={() => open(n)}
					>
						<span class="mt-0.5 rounded-full p-1.5 {s.cls}" aria-hidden="true">
							<s.icon class="size-4" />
						</span>
						<span class="min-w-0 flex-1">
							<span class="flex flex-wrap items-center gap-x-2">
								<span class="text-sm {n.read_at ? 'text-fg' : 'font-semibold text-fg'}"
									>{n.title}</span
								>
								<span class="sr-only">{s.label}{n.read_at ? '' : ', unread'}</span>
								<span class="text-xs text-fg-subtle"
									>{n.type === 'gpu' ? 'GPU' : titleCase(n.type)}</span
								>
							</span>
							{#if n.body}
								<span class="mt-0.5 block text-sm whitespace-pre-line text-fg-muted">{n.body}</span>
							{/if}
						</span>
						<span class="flex shrink-0 flex-col items-end gap-1 text-xs text-fg-muted">
							<time datetime={n.created_at} title={dateTime(n.created_at)}
								>{relative(n.created_at)}</time
							>
							<span class="flex items-center gap-1.5">
								{#if n.link}<ExternalLink class="size-3.5" aria-label="Opens a link" />{/if}
								{#if !n.read_at}<span class="size-2 rounded-full bg-accent" aria-hidden="true"
									></span>{/if}
							</span>
						</span>
					</button>
				</li>
			{/each}
		</ul>
		{#if total > perPage}
			<div class="border-t border-border px-4">
				<Pagination bind:page={pageNo} {perPage} {total} />
			</div>
		{/if}
	{/if}
</Card>
