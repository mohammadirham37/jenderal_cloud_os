<script lang="ts">
	import { Megaphone, Plus, Trash2 } from '@lucide/svelte';
	import {
		Badge,
		Button,
		Card,
		DataTable,
		EmptyState,
		Field,
		Modal,
		PageHeader
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Announcement } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime, titleCase } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';

	const allowed = $derived(auth.can('announcements.manage'));
	const list = resource(
		guarded(['announcements.manage'], () =>
			api.get<Announcement[]>('/announcements', { all: 'true' })
		)
	);
	const sevTone = { info: 'info', warning: 'warning', critical: 'danger' } as const;

	function visibility(a: Announcement): { label: string; tone: 'success' | 'info' | 'neutral' } {
		const now = Date.now();
		if (new Date(a.starts_at).getTime() > now) return { label: 'Scheduled', tone: 'info' };
		if (a.ends_at && new Date(a.ends_at).getTime() <= now)
			return { label: 'Ended', tone: 'neutral' };
		return { label: 'Visible', tone: 'success' };
	}

	// Create
	let open = $state(false);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let form = $state(blank());
	function blank() {
		return {
			title: '',
			body: '',
			severity: 'info' as 'info' | 'warning' | 'critical',
			starts_at: '',
			ends_at: '',
			notify: false
		};
	}
	function openCreate() {
		form = blank();
		errors = {};
		open = true;
	}
	/** datetime-local value (local time) → ISO 8601 UTC. */
	const toISO = (v: string) => (v ? new Date(v).toISOString() : null);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		const starts = toISO(form.starts_at);
		const ends = toISO(form.ends_at);
		if (ends && new Date(ends).getTime() <= new Date(starts ?? Date.now()).getTime()) {
			errors = { ends_at: 'Must be after the start time.' };
			return;
		}
		saving = true;
		try {
			await api.post('/announcements', {
				title: form.title.trim(),
				body: form.body.trim(),
				severity: form.severity,
				starts_at: starts,
				ends_at: ends,
				notify: form.notify
			});
			toast.success(
				'Announcement published',
				form.notify ? 'All users were notified.' : form.title.trim()
			);
			open = false;
			list.reload();
		} catch (err) {
			errors = handleFormError(err, 'Could not publish the announcement');
		} finally {
			saving = false;
		}
	}

	async function remove(a: Announcement) {
		const ok = await confirmDialog.confirm({
			title: `Delete “${a.title}”?`,
			message:
				'The announcement disappears from every dashboard. Notifications already sent are kept.',
			danger: true,
			confirmLabel: 'Delete announcement'
		});
		if (!ok) return;
		try {
			await api.del(`/announcements/${a.id}`);
			toast.success('Announcement deleted', a.title);
			list.reload();
		} catch (err) {
			toast.error('Could not delete the announcement', errorMessage(err));
		}
	}

	const columns = [
		{ key: 'title', label: 'Announcement' },
		{ key: 'severity', label: 'Severity' },
		{ key: 'window', label: 'Visible' },
		{ key: 'state', label: 'State' },
		{ key: 'by', label: 'Created by' },
		{ key: 'actions', label: 'Actions', class: 'text-right' }
	];
</script>

<PageHeader
	title="Announcements"
	description="Messages shown on every user’s dashboard during their visibility window (latest 50)."
>
	{#snippet actions()}
		{#if allowed}
			<Button variant="primary" onclick={openCreate}
				><Plus class="size-4" /> New announcement</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	<Card padded={false}>
		<DataTable
			{columns}
			rows={list.data ?? []}
			loading={list.loading}
			error={list.error}
			retry={list.reload}
		>
			{#snippet empty()}
				<EmptyState
					icon={Megaphone}
					title="No announcements"
					description="Announce maintenance windows, outages or policy changes to all users."
				>
					{#snippet action()}<Button variant="primary" onclick={openCreate}
							><Plus class="size-4" /> New announcement</Button
						>{/snippet}
				</EmptyState>
			{/snippet}
			{#snippet row(a)}
				{@const st = visibility(a)}
				<tr class="align-top">
					<td class="px-4 py-3">
						<p class="font-medium">{a.title}</p>
						{#if a.body}<p
								class="mt-0.5 line-clamp-2 max-w-xl text-xs whitespace-pre-line text-fg-muted"
							>
								{a.body}
							</p>{/if}
					</td>
					<td class="px-4 py-3"
						><Badge tone={sevTone[a.severity]} dot>{titleCase(a.severity)}</Badge></td
					>
					<td class="px-4 py-3 text-xs whitespace-nowrap">
						from {dateTime(a.starts_at)}<br />
						<span class="text-fg-muted"
							>{a.ends_at ? `until ${dateTime(a.ends_at)}` : 'no end'}</span
						>
					</td>
					<td class="px-4 py-3"><Badge tone={st.tone}>{st.label}</Badge></td>
					<td class="px-4 py-3 text-xs text-fg-muted">
						{a.created_by_username ?? '—'}<br />{dateTime(a.created_at)}
					</td>
					<td class="px-4 py-3 text-right">
						<Button
							size="sm"
							variant="ghost"
							onclick={() => remove(a)}
							aria-label="Delete {a.title}"><Trash2 class="size-3.5 text-danger" /></Button
						>
					</td>
				</tr>
			{/snippet}
		</DataTable>
	</Card>
{/if}

<Modal bind:open title="New announcement" size="lg">
	<form id="ann-form" class="grid gap-4 sm:grid-cols-2" onsubmit={submit}>
		<div class="sm:col-span-2">
			<Field label="Title" id="a-title" required error={errors.title}>
				<input id="a-title" class="input" bind:value={form.title} maxlength="200" required />
			</Field>
		</div>
		<div class="sm:col-span-2">
			<Field
				label="Message"
				id="a-body"
				error={errors.body}
				hint="Plain text, up to 5000 characters."
			>
				<textarea id="a-body" class="input" rows="5" maxlength="5000" bind:value={form.body}
				></textarea>
			</Field>
		</div>
		<Field label="Severity" id="a-sev" error={errors.severity}>
			<select id="a-sev" class="input" bind:value={form.severity}>
				<option value="info">Info</option>
				<option value="warning">Warning</option>
				<option value="critical">Critical</option>
			</select>
		</Field>
		<div></div>
		<Field
			label="Starts at"
			id="a-start"
			error={errors.starts_at}
			hint="Your local time. Empty = immediately."
		>
			<input id="a-start" type="datetime-local" class="input" bind:value={form.starts_at} />
		</Field>
		<Field
			label="Ends at"
			id="a-end"
			error={errors.ends_at}
			hint="Your local time. Empty = no end."
		>
			<input id="a-end" type="datetime-local" class="input" bind:value={form.ends_at} />
		</Field>
		<label class="flex items-start gap-2 text-sm sm:col-span-2">
			<input type="checkbox" class="mt-0.5" bind:checked={form.notify} />
			<span>
				Also send a notification to all users
				<span class="block text-xs text-fg-muted"
					>Sent immediately, even when the start time is later.</span
				>
			</span>
		</label>
	</form>
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="ann-form" variant="primary" loading={saving}>Publish</Button>
	{/snippet}
</Modal>
