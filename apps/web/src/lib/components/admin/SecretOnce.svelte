<script lang="ts">
	// Displays a secret (temporary password, reset link) exactly once with a
	// copy button and a warning that it cannot be shown again.
	import { Alert, CopyButton } from '#lib/components/ui/index.ts';

	let {
		label,
		value,
		warning = 'Copy it now. It will not be shown again.'
	}: { label: string; value: string; warning?: string } = $props();
</script>

<div class="space-y-3">
	<Alert tone="warning" title="Shown only once">{warning}</Alert>
	<div>
		<p class="label">{label}</p>
		<div class="flex items-center gap-2 rounded-md border border-border bg-panel-2 px-3 py-2">
			<code class="min-w-0 flex-1 font-mono text-sm break-all select-all">{value}</code>
			<CopyButton text={value} label="Copy {label.toLowerCase()}" />
		</div>
	</div>
</div>
