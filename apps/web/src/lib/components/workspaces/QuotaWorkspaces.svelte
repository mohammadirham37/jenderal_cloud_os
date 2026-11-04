<script lang="ts">
	// Breaks a user's storage quota down per workspace, so it is clear what
	// holds the allocation. Failed workspaces get a one-click delete because
	// they keep their storage reserved without being usable.
	import { Trash2 } from '@lucide/svelte';
	import { api } from '#lib/api/client.ts';
	import type { UsageWorkspace } from '#lib/api/types-workspaces.ts';
	import { Button, StatusBadge } from '#lib/components/ui/index.ts';
	import { canDelete, toastError } from '#lib/components/workspaces/workspace.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes } from '#lib/format.ts';

	let {
		workspaces,
		storageQuota,
		ondeleted
	}: {
		workspaces: UsageWorkspace[];
		storageQuota: number;
		ondeleted?: () => void;
	} = $props();

	let busy = $state<Record<string, boolean>>({});
	const failed = $derived(workspaces.filter((w) => w.status === 'failed'));
	const failedBytes = $derived(failed.reduce((n, w) => n + w.storage_bytes, 0));

	const pct = (n: number) => (storageQuota > 0 ? Math.min(100, (n / storageQuota) * 100) : 0);

	async function remove(w: UsageWorkspace) {
		const ok = await confirmDialog.confirm({
			title: `Delete ${w.name}?`,
			message:
				w.status === 'failed'
					? `This workspace failed and cannot run. Deleting it frees ${bytes(w.storage_bytes)} of storage quota.`
					: `The workspace and all files in its volume are deleted. This frees ${bytes(w.storage_bytes)} of storage quota.`,
			confirmLabel: 'Delete workspace',
			danger: true
		});
		if (!ok) return;
		busy[w.id] = true;
		try {
			await api.del(`/workspaces/${w.id}`, { reason: 'freeing storage quota' });
			toast.success(
				'Deleting workspace',
				`${bytes(w.storage_bytes)} of storage quota will be freed.`
			);
			ondeleted?.();
		} catch (e) {
			toastError(`Could not delete ${w.name}`, e);
		} finally {
			busy[w.id] = false;
		}
	}
</script>

{#if workspaces.length === 0}
	<p class="text-sm text-fg-muted">No workspaces hold storage quota.</p>
{:else}
	{#if failed.length > 0}
		<p
			class="mb-3 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-fg"
			role="status"
		>
			{failed.length === 1
				? '1 failed workspace reserves'
				: `${failed.length} failed workspaces reserve`}
			<strong>{bytes(failedBytes)}</strong> without being usable. Delete {failed.length === 1
				? 'it'
				: 'them'} to free the quota.
		</p>
	{/if}
	<ul class="divide-y divide-border" aria-label="Storage by workspace">
		{#each workspaces as w (w.id)}
			<li class="flex items-center gap-3 py-2.5">
				<div class="min-w-0 flex-1">
					<div class="flex items-center gap-2">
						<a href="/workspaces/{w.id}" class="truncate font-medium text-fg hover:underline"
							>{w.name}</a
						>
						<StatusBadge status={w.status} />
					</div>
					<div class="mt-1.5 flex items-center gap-2">
						<div
							class="h-1.5 flex-1 overflow-hidden rounded-full bg-panel-2"
							title="{bytes(w.storage_bytes)} of {bytes(storageQuota)}"
						>
							<div
								class="h-full rounded-full {w.status === 'failed' ? 'bg-warning' : 'bg-accent'}"
								style:width="{pct(w.storage_bytes)}%"
							></div>
						</div>
						<span
							class="shrink-0 text-right text-xs whitespace-nowrap text-fg-muted tabular-nums sm:w-52"
						>
							<strong class="text-fg">{bytes(w.storage_bytes)}</strong>
							reserved{#if w.storage_used_bytes !== null}
								&nbsp;· {bytes(w.storage_used_bytes)} used{/if}
						</span>
					</div>
					<p class="mt-0.5 text-xs text-fg-subtle">{w.template_name}</p>
				</div>
				{#if canDelete(w.status)}
					<Button
						size="sm"
						variant={w.status === 'failed' ? 'danger' : 'ghost'}
						loading={busy[w.id]}
						onclick={() => remove(w)}
						aria-label="Delete {w.name}"
					>
						<Trash2 class="size-3.5" />
						{#if w.status === 'failed'}Delete{/if}
					</Button>
				{/if}
			</li>
		{/each}
	</ul>
{/if}
