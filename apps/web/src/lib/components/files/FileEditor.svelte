<script lang="ts">
	// Plain-text editor with a line-number gutter. Tab inserts two spaces
	// (press Esc first to move focus with Tab), Ctrl/Cmd+S saves.
	import { Save, X, RotateCcw, Download } from '@lucide/svelte';
	import { Alert, Badge, Button } from '#lib/components/ui/index.ts';
	import { bytes, relative } from '#lib/format.ts';

	let {
		path,
		value = $bindable(),
		dirty,
		saving = false,
		size,
		modTime,
		conflict = null,
		downloadHref,
		onsave,
		onclose,
		onreload,
		onoverwrite,
		ondownload
	}: {
		path: string;
		value: string;
		dirty: boolean;
		saving?: boolean;
		size: number;
		modTime: string;
		/** Message shown when the file changed on disk since it was opened. */
		conflict?: string | null;
		downloadHref?: string;
		onsave: () => void;
		onclose: () => void;
		onreload: () => void;
		onoverwrite: () => void;
		ondownload?: () => void;
	} = $props();

	let textarea = $state<HTMLTextAreaElement>();
	let gutter = $state<HTMLPreElement>();
	let escaped = false;

	const lineCount = $derived(value.split('\n').length);
	const numbers = $derived(Array.from({ length: lineCount }, (_, i) => i + 1).join('\n'));

	function onkeydown(e: KeyboardEvent) {
		if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
			e.preventDefault();
			if (!saving) onsave();
			return;
		}
		if (e.key === 'Escape') {
			escaped = true;
			return;
		}
		if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && !escaped) {
			e.preventDefault();
			const ta = e.currentTarget as HTMLTextAreaElement;
			ta.setRangeText('  ', ta.selectionStart, ta.selectionEnd, 'end');
			value = ta.value;
			return;
		}
		escaped = false;
	}

	function syncScroll() {
		if (gutter && textarea) gutter.scrollTop = textarea.scrollTop;
	}

	$effect(() => {
		textarea?.focus();
	});
</script>

<section class="card flex flex-col overflow-hidden" aria-label="Editor for {path}">
	<header
		class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3"
	>
		<div class="min-w-0">
			<h2 class="flex items-center gap-2 truncate font-mono text-sm font-semibold text-fg">
				{path}
				{#if dirty}<Badge tone="warning" dot>Unsaved changes</Badge>{/if}
			</h2>
			<p class="mt-0.5 text-xs text-fg-muted">
				{bytes(size)} on disk · modified {relative(modTime)} · {lineCount}
				{lineCount === 1 ? 'line' : 'lines'}
			</p>
		</div>
		<div class="flex flex-wrap items-center gap-2">
			{#if downloadHref}
				<a
					href={downloadHref}
					download
					class="inline-flex h-8 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-fg-muted hover:bg-panel-2 hover:text-fg"
				>
					<Download class="size-3.5" aria-hidden="true" /> Download
				</a>
			{:else if ondownload}
				<Button size="sm" variant="ghost" onclick={ondownload}>
					<Download class="size-3.5" /> Download
				</Button>
			{/if}
			<Button size="sm" onclick={onclose}><X class="size-3.5" /> Close</Button>
			<Button
				size="sm"
				variant="primary"
				onclick={onsave}
				loading={saving}
				disabled={!dirty && !conflict}
				title="Save (Ctrl/Cmd+S)"
			>
				<Save class="size-3.5" /> Save
			</Button>
		</div>
	</header>

	{#if conflict}
		<div class="border-b border-border p-3">
			<Alert tone="warning" title="The file changed on disk">
				<p>{conflict}</p>
				<p class="mt-1">
					Reload to get the latest version (your changes are discarded), or overwrite the file with
					your version.
				</p>
				<div class="mt-2 flex flex-wrap gap-2">
					<Button size="sm" onclick={onreload}
						><RotateCcw class="size-3.5" /> Reload from disk</Button
					>
					<Button size="sm" variant="danger" onclick={onoverwrite} loading={saving}>
						Overwrite with my version
					</Button>
				</div>
			</Alert>
		</div>
	{/if}

	<div class="flex min-h-[60vh] flex-1 bg-panel font-mono text-[13px] leading-5">
		<pre
			bind:this={gutter}
			class="m-0 h-[calc(100vh-18rem)] min-h-[60vh] overflow-hidden border-r border-border bg-panel-2/60 px-3 py-3 text-right text-fg-subtle select-none"
			aria-hidden="true">{numbers}</pre>
		<label for="file-editor" class="sr-only">Contents of {path}</label>
		<textarea
			id="file-editor"
			bind:this={textarea}
			bind:value
			{onkeydown}
			onscroll={syncScroll}
			wrap="off"
			spellcheck="false"
			autocapitalize="off"
			autocomplete="off"
			aria-describedby="file-editor-help"
			class="m-0 h-[calc(100vh-18rem)] min-h-[60vh] flex-1 resize-none overflow-auto border-0 bg-transparent px-3 py-3 font-mono text-[13px] leading-5 whitespace-pre text-fg focus:ring-0 focus:outline-none"
		></textarea>
	</div>
	<p id="file-editor-help" class="border-t border-border px-4 py-2 text-xs text-fg-muted">
		<span class="kbd">Tab</span> inserts two spaces · press <span class="kbd">Esc</span> then
		<span class="kbd">Tab</span> to leave the editor · <span class="kbd">Ctrl/Cmd+S</span> saves
	</p>
</section>
