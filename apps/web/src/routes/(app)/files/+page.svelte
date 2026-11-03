<script lang="ts">
	import { FolderOpen, HardDrive, Layers } from '@lucide/svelte';
	import {
		Button,
		Card,
		EmptyState,
		ErrorState,
		PageHeader,
		Skeleton,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Workspace } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { bytes, relative } from '#lib/format.ts';

	// Files launcher: pick a workspace whose storage you can edit.
	const list = resource(() =>
		api.list<Workspace>('/workspaces', { per_page: 100, sort: '-updated_at' })
	);
	const usable = $derived(
		(list.data?.data ?? []).filter(
			(w) =>
				(w.access === 'owner' || w.access === 'editor') &&
				['ready', 'running', 'stopped'].includes(w.status) &&
				(w.owner_id === auth.me?.id || w.access === 'editor')
		)
	);
</script>

<PageHeader title="Files" description="Choose a workspace to browse, upload and edit its files." />

{#if list.error}
	<Card><ErrorState error={list.error} retry={list.reload} /></Card>
{:else if !list.data}
	<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
		{#each [1, 2, 3] as i (i)}<div class="card p-4"><Skeleton lines={3} /></div>{/each}
	</div>
{:else if usable.length === 0}
	<Card>
		<EmptyState
			icon={FolderOpen}
			title="No workspace storage available"
			description="Files live inside workspaces. Create a workspace (or wait for provisioning to finish) to manage its files."
		>
			{#snippet action()}
				{#if auth.can('workspaces.create')}<Button href="/workspaces/new" variant="primary"
						>Create workspace</Button
					>{/if}
			{/snippet}
		</EmptyState>
	</Card>
{:else}
	<ul class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
		{#each usable as w (w.id)}
			<li>
				<a
					href="/workspaces/{w.id}/files"
					class="group card flex h-full flex-col gap-3 p-4 transition hover:-translate-y-0.5 hover:border-accent hover:shadow-md"
				>
					<div class="flex items-start justify-between gap-2">
						<span
							class="flex size-10 items-center justify-center rounded-lg bg-[#d97706]/15 text-[#d97706]"
						>
							<FolderOpen class="size-5" />
						</span>
						<StatusBadge status={w.status} pending={w.pending_job} />
					</div>
					<div class="min-w-0">
						<p class="truncate font-medium group-hover:text-accent">{w.name}</p>
						<p class="mt-0.5 flex items-center gap-1 truncate text-xs text-fg-muted">
							<Layers class="size-3" />{w.template_name}{#if w.owner_id !== auth.me?.id}
								· {w.owner_username}{/if}
						</p>
					</div>
					<p class="mt-auto flex items-center gap-1.5 text-xs text-fg-muted">
						<HardDrive class="size-3.5" />
						{w.storage_used_bytes === null
							? 'Usage not measured yet'
							: `${bytes(w.storage_used_bytes)} of ${bytes(w.storage_bytes)}`}
						· {relative(w.updated_at)}
					</p>
				</a>
			</li>
		{/each}
	</ul>
{/if}
