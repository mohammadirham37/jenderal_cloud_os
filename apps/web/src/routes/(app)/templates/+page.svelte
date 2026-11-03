<script lang="ts">
	import { LayoutTemplate, Plus } from '@lucide/svelte';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Template } from '#lib/api/types.ts';
	import {
		Button,
		Card,
		EmptyState,
		ErrorState,
		PageHeader,
		SearchInput,
		Skeleton
	} from '#lib/components/ui/index.ts';
	import TemplateCard from '#lib/components/workspaces/TemplateCard.svelte';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { titleCase } from '#lib/format.ts';

	const templates = resource(() => api.get<Template[]>('/templates'));
	let q = $state('');

	const groups = $derived.by(() => {
		const needle = q.trim().toLowerCase();
		const map: Record<string, Template[]> = {};
		for (const t of templates.data ?? []) {
			if (
				needle &&
				![t.name, t.key, t.description, t.category, t.image_reference].some((s) =>
					s?.toLowerCase().includes(needle)
				)
			)
				continue;
			(map[t.category] ??= []).push(t);
		}
		return Object.entries(map);
	});
	const canCreate = $derived(auth.can('workspaces.create'));
</script>

<PageHeader
	title="Templates"
	description="Ready-to-use development environments. Pick one to create a workspace."
>
	{#snippet actions()}
		<div class="w-64"><SearchInput bind:value={q} placeholder="Filter templates" /></div>
	{/snippet}
</PageHeader>

{#if templates.error}
	<Card><ErrorState error={templates.error} retry={templates.reload} /></Card>
{:else if !templates.data}
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
		{#each [1, 2, 3] as i (i)}<div class="card p-4"><Skeleton lines={4} /></div>{/each}
	</div>
{:else if templates.data.length === 0}
	<Card>
		<EmptyState
			icon={LayoutTemplate}
			title="No templates available"
			description="An administrator has not published any templates yet, or their images are not on the allowlist."
		/>
	</Card>
{:else if groups.length === 0}
	<Card>
		<EmptyState title="No matching templates" description="Try a different search term.">
			{#snippet action()}<Button onclick={() => (q = '')}>Clear filter</Button>{/snippet}
		</EmptyState>
	</Card>
{:else}
	<div class="space-y-8">
		{#each groups as [category, list] (category)}
			<section aria-labelledby="cat-{category}">
				<h2
					id="cat-{category}"
					class="mb-3 text-xs font-semibold tracking-wide text-fg-muted uppercase"
				>
					{titleCase(category || 'other')}
					<span class="font-normal">({list.length})</span>
				</h2>
				<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
					{#each list as t (t.id)}
						<TemplateCard template={t}>
							{#snippet actions()}
								{#if canCreate}
									<Button size="sm" variant="primary" href="/workspaces/new?template={t.key}">
										<Plus class="size-3.5" /> Use template
									</Button>
								{/if}
							{/snippet}
						</TemplateCard>
					{/each}
				</div>
			</section>
		{/each}
	</div>
{/if}
