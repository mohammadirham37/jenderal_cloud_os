<script lang="ts">
	import { Monitor } from '@lucide/svelte';
	import { Badge, Button, Card, DataTable, EmptyState } from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { dateTime, relative } from '#lib/format.ts';

	interface Session {
		id: string;
		ip: string | null;
		user_agent: string | null;
		created_at: string;
		last_seen_at: string;
		expires_at: string;
		current: boolean;
	}

	const sessions = resource(() => api.get<Session[]>('/auth/sessions'));
	const others = $derived((sessions.data ?? []).filter((s) => !s.current));
	let busy = $state<string | null>(null);

	/** Short, human description of a user agent string (best effort). */
	function describe(ua: string | null): string {
		if (!ua) return 'Unknown device';
		const browser = /Edg\//.test(ua)
			? 'Edge'
			: /Firefox\//.test(ua)
				? 'Firefox'
				: /Chrome\//.test(ua)
					? 'Chrome'
					: /Safari\//.test(ua)
						? 'Safari'
						: /curl|Go-http|python/i.test(ua)
							? 'API client'
							: 'Browser';
		const os = /Windows/.test(ua)
			? 'Windows'
			: /Android/.test(ua)
				? 'Android'
				: /iPhone|iPad/.test(ua)
					? 'iOS'
					: /Mac OS X/.test(ua)
						? 'macOS'
						: /Linux/.test(ua)
							? 'Linux'
							: '';
		return os ? `${browser} on ${os}` : browser;
	}

	async function revoke(s: Session) {
		const ok = await confirmDialog.confirm({
			title: 'Revoke session?',
			message: `${describe(s.user_agent)}${s.ip ? ` (${s.ip})` : ''} will be signed out immediately.`,
			confirmLabel: 'Revoke',
			danger: true
		});
		if (!ok) return;
		busy = s.id;
		try {
			await api.del(`/auth/sessions/${s.id}`);
			toast.success('Session revoked');
			await sessions.reload();
		} catch (err) {
			toast.error('Could not revoke the session', errorMessage(err));
		} finally {
			busy = null;
		}
	}

	async function revokeOthers() {
		const ok = await confirmDialog.confirm({
			title: 'Sign out all other sessions?',
			message: `${others.length} other session${others.length === 1 ? '' : 's'} will be signed out. This session stays active.`,
			confirmLabel: 'Sign out others',
			danger: true
		});
		if (!ok) return;
		busy = 'all';
		let failed = 0;
		for (const s of others) {
			try {
				await api.del(`/auth/sessions/${s.id}`);
			} catch {
				failed++;
			}
		}
		busy = null;
		if (failed) toast.error('Some sessions could not be revoked', `${failed} failed.`);
		else toast.success('Other sessions signed out');
		await sessions.reload();
	}
</script>

<Card
	title="Active sessions"
	description="Devices currently signed in to your account."
	padded={false}
>
	{#snippet actions()}
		<Button
			size="sm"
			onclick={revokeOthers}
			disabled={others.length === 0 || busy !== null}
			loading={busy === 'all'}>Sign out other sessions</Button
		>
	{/snippet}
	<DataTable
		columns={[
			{ key: 'device', label: 'Device' },
			{ key: 'ip', label: 'IP address' },
			{ key: 'created', label: 'Signed in' },
			{ key: 'seen', label: 'Last active' },
			{ key: 'expires', label: 'Expires' },
			{ key: 'actions', label: 'Actions', class: 'text-right' }
		]}
		rows={sessions.data ?? []}
		loading={sessions.loading}
		error={sessions.error}
		retry={sessions.reload}
	>
		{#snippet row(s: Session)}
			<tr>
				<td class="px-4 py-3">
					<p class="flex items-center gap-2 font-medium">
						{describe(s.user_agent)}
						{#if s.current}<Badge tone="success">This session</Badge>{/if}
					</p>
					<p class="max-w-80 truncate text-xs text-fg-subtle" title={s.user_agent ?? ''}>
						{s.user_agent ?? '—'}
					</p>
				</td>
				<td class="px-4 py-3 font-mono text-xs">{s.ip ?? '—'}</td>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted" title={dateTime(s.created_at)}
					>{relative(s.created_at)}</td
				>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted" title={dateTime(s.last_seen_at)}
					>{relative(s.last_seen_at)}</td
				>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted" title={dateTime(s.expires_at)}
					>{relative(s.expires_at)}</td
				>
				<td class="px-4 py-3 text-right">
					{#if !s.current}
						<Button
							size="sm"
							onclick={() => revoke(s)}
							loading={busy === s.id}
							disabled={busy !== null}>Revoke</Button
						>
					{/if}
				</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			<EmptyState icon={Monitor} title="No active sessions" />
		{/snippet}
	</DataTable>
</Card>
