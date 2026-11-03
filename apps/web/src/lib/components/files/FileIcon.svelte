<script lang="ts">
	import {
		File,
		FileArchive,
		FileCode,
		FileImage,
		FileJson,
		FileQuestion,
		FileSpreadsheet,
		FileText,
		Folder,
		Link
	} from '@lucide/svelte';
	import type { FileEntry } from '#lib/api/types.ts';

	let { entry, class: klass = 'size-4' }: { entry: FileEntry; class?: string } = $props();

	const code = new Set([
		'go',
		'py',
		'js',
		'ts',
		'jsx',
		'tsx',
		'svelte',
		'vue',
		'php',
		'java',
		'kt',
		'c',
		'h',
		'cpp',
		'hpp',
		'cc',
		'rs',
		'rb',
		'sh',
		'bash',
		'zsh',
		'html',
		'htm',
		'css',
		'scss',
		'sql',
		'r',
		'm',
		'xml',
		'ipynb'
	]);
	const images = new Set(['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'bmp', 'ico']);
	const archives = new Set(['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar']);
	const sheets = new Set(['csv', 'tsv', 'xls', 'xlsx', 'ods']);
	const texts = new Set(['txt', 'md', 'log', 'tex', 'bib', 'ini', 'cfg', 'conf', 'env']);

	const Icon = $derived.by(() => {
		if (entry.type === 'dir') return Folder;
		if (entry.type === 'symlink') return Link;
		if (entry.type === 'other') return FileQuestion;
		const ext = entry.name.includes('.') ? entry.name.split('.').pop()!.toLowerCase() : '';
		if (ext === 'json' || ext === 'yaml' || ext === 'yml' || ext === 'toml') return FileJson;
		if (code.has(ext)) return FileCode;
		if (images.has(ext)) return FileImage;
		if (archives.has(ext)) return FileArchive;
		if (sheets.has(ext)) return FileSpreadsheet;
		if (texts.has(ext)) return FileText;
		return File;
	});
</script>

<Icon
	class="{klass} shrink-0 {entry.type === 'dir' ? 'text-accent' : 'text-fg-subtle'}"
	aria-hidden="true"
/>
