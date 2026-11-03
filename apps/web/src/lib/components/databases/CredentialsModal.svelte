<script lang="ts">
	// Shows freshly generated database credentials exactly once. The dialog
	// cannot be dismissed until the user confirms the password was saved.
	import { Alert, Button, CopyButton, Modal } from '#lib/components/ui/index.ts';
	import type { DatabaseCredentials } from '#lib/api/types.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';

	let {
		credentials = $bindable(null),
		title = 'Database credentials'
	}: { credentials?: DatabaseCredentials | null; title?: string } = $props();

	let open = $state(false);
	let acknowledged = $state(false);

	$effect(() => {
		if (credentials) {
			open = true;
			acknowledged = false;
		}
	});

	const rows = $derived(
		credentials
			? [
					{ label: 'Host', value: credentials.host },
					{ label: 'Port', value: String(credentials.port) },
					{ label: 'Database', value: credentials.database },
					{ label: 'Username', value: credentials.username },
					{ label: 'Password', value: credentials.password },
					{ label: 'Connection string', value: credentials.connection_string }
				]
			: []
	);

	function onclose() {
		if (!acknowledged) {
			// Keep the dialog open: the password cannot be retrieved later.
			open = true;
			toast.warning(
				'Save the password first',
				'Confirm that you have stored the credentials before closing this dialog.'
			);
			return;
		}
		credentials = null;
	}

	function done() {
		open = false;
		credentials = null;
	}
</script>

{#if credentials}
	<Modal bind:open {title} size="lg" {onclose}>
		<Alert tone="warning" title="The password will not be shown again">
			Copy the password or connection string now and store it in a password manager or your
			application's secret store. If you lose it you can only reset it, which invalidates the old
			password.
		</Alert>
		<dl class="mt-4 divide-y divide-border rounded-md border border-border">
			{#each rows as r (r.label)}
				<div class="flex items-center gap-3 px-3 py-2">
					<dt class="w-36 shrink-0 text-sm text-fg-muted">{r.label}</dt>
					<dd class="min-w-0 flex-1 font-mono text-sm break-all text-fg">{r.value}</dd>
					<CopyButton text={r.value} label="Copy {r.label.toLowerCase()}" />
				</div>
			{/each}
		</dl>
		<label class="mt-4 flex items-center gap-2 text-sm">
			<input
				type="checkbox"
				class="rounded border-border-strong text-accent"
				bind:checked={acknowledged}
			/>
			I have saved the password in a safe place.
		</label>
		{#snippet footer()}
			<Button variant="primary" disabled={!acknowledged} onclick={done}>Done</Button>
		{/snippet}
	</Modal>
{/if}
