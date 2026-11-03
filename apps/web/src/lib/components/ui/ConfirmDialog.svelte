<script lang="ts">
	import Modal from './Modal.svelte';
	import Button from './Button.svelte';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';

	let typed = $state('');
	let reason = $state('');
	const c = $derived(confirmDialog.current);
	const canConfirm = $derived(
		!!c &&
			(!c.requireText || typed === c.requireText) &&
			(!c.reasonRequired || reason.trim().length >= (c.reasonMinLength ?? 3))
	);
	let open = $state(false);
	$effect(() => {
		open = c !== null;
		if (c) {
			typed = '';
			reason = '';
		}
	});
</script>

{#if c}
	<Modal bind:open title={c.title} size="sm" onclose={() => confirmDialog.close(false)}>
		{#if c.message}<p class="text-sm text-fg-muted">{c.message}</p>{/if}
		{#if c.reasonLabel}
			<div class="mt-4">
				<label for="confirm-reason" class="label">{c.reasonLabel}</label>
				<textarea id="confirm-reason" class="input" rows="3" bind:value={reason}></textarea>
				{#if c.reasonRequired && (c.reasonMinLength ?? 3) > 3}
					<p class="mt-1 text-xs text-fg-muted">At least {c.reasonMinLength} characters.</p>
				{/if}
			</div>
		{/if}
		{#if c.requireText}
			<div class="mt-4">
				<label for="confirm-text" class="label">
					Type <span class="kbd">{c.requireText}</span> to confirm
				</label>
				<input id="confirm-text" class="input font-mono" bind:value={typed} autocomplete="off" />
			</div>
		{/if}
		{#snippet footer()}
			<Button onclick={() => confirmDialog.close(false)}>{c.cancelLabel ?? 'Cancel'}</Button>
			<Button
				variant={c.danger ? 'danger' : 'primary'}
				disabled={!canConfirm}
				onclick={() => confirmDialog.close(true, reason.trim())}
				>{c.confirmLabel ?? 'Confirm'}</Button
			>
		{/snippet}
	</Modal>
{/if}
