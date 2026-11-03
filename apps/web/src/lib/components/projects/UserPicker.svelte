<script lang="ts">
	// Username input with suggestions from GET /users/lookup. Falls back to a
	// plain username field when the lookup is not permitted (the backend also
	// accepts a username when adding members).
	import { api, ApiError } from '#lib/api/client.ts';

	interface LookupUser {
		id: string;
		username: string;
		full_name: string;
	}

	let {
		value = $bindable(''),
		id,
		invalid = false,
		exclude = [],
		placeholder = 'Search by username or name'
	}: {
		value?: string;
		id: string;
		invalid?: boolean;
		exclude?: string[];
		placeholder?: string;
	} = $props();

	let results = $state<LookupUser[]>([]);
	let open = $state(false);
	let active = $state(-1);
	let searching = $state(false);
	let lookupDisabled = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const visible = $derived(results.filter((u) => !exclude.includes(u.id)));

	async function search(q: string) {
		const my = ++seq;
		searching = true;
		try {
			const r = await api.get<LookupUser[]>('/users/lookup', { q });
			if (my !== seq) return;
			results = r;
			active = -1;
			open = true;
		} catch (e) {
			if (my !== seq) return;
			if (e instanceof ApiError && e.status === 403) lookupDisabled = true;
			results = [];
			open = false;
		} finally {
			if (my === seq) searching = false;
		}
	}

	function oninput() {
		clearTimeout(timer);
		const q = value.trim();
		if (q.length < 2 || lookupDisabled) {
			seq++;
			results = [];
			open = false;
			return;
		}
		timer = setTimeout(() => search(q), 250);
	}

	function choose(u: LookupUser) {
		value = u.username;
		open = false;
		results = [];
	}

	function onkeydown(e: KeyboardEvent) {
		if (!open || visible.length === 0) return;
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			active = (active + 1) % visible.length;
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			active = (active - 1 + visible.length) % visible.length;
		} else if (e.key === 'Enter' && active >= 0) {
			e.preventDefault();
			choose(visible[active]);
		} else if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			open = false;
		}
	}
</script>

<div class="relative">
	<input
		{id}
		class="input"
		role="combobox"
		aria-expanded={open && visible.length > 0}
		aria-controls="{id}-listbox"
		aria-autocomplete="list"
		aria-activedescendant={active >= 0 ? `${id}-opt-${active}` : undefined}
		aria-invalid={invalid}
		aria-busy={searching}
		autocomplete="off"
		{placeholder}
		bind:value
		{oninput}
		{onkeydown}
		onblur={() => setTimeout(() => (open = false), 150)}
	/>
	{#if open}
		<ul
			id="{id}-listbox"
			role="listbox"
			class="absolute z-20 mt-1 max-h-60 w-full overflow-auto rounded-md border border-border bg-panel py-1 shadow-lg"
		>
			{#each visible as u, i (u.id)}
				<li
					id="{id}-opt-{i}"
					role="option"
					tabindex="-1"
					aria-selected={i === active}
					class="cursor-pointer px-3 py-2 text-sm hover:bg-panel-2 {i === active
						? 'bg-panel-2'
						: ''}"
					onmousedown={(e) => {
						e.preventDefault();
						choose(u);
					}}
				>
					<span class="font-medium text-fg">{u.username}</span>
					{#if u.full_name}<span class="text-fg-muted"> · {u.full_name}</span>{/if}
				</li>
			{:else}
				<li class="px-3 py-2 text-sm text-fg-muted">No matching active users.</li>
			{/each}
		</ul>
	{/if}
</div>
