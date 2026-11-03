<script lang="ts">
	import { Check, Circle } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Alert, Button, Card, Field, PageHeader } from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';

	const required = $derived(
		page.url.searchParams.get('required') === '1' || !!auth.me?.must_change_password
	);

	let current = $state('');
	let next = $state('');
	let repeat = $state('');
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');

	const classes = $derived(
		[/[a-z]/, /[A-Z]/, /[0-9]/, /[^a-zA-Z0-9]/].filter((r) => r.test(next)).length
	);
	const checks = $derived([
		{ ok: next.length >= 12, label: 'At least 12 characters' },
		{
			ok: classes >= 3,
			label: 'At least 3 of: lowercase, uppercase, digits, symbols'
		},
		{ ok: next !== '' && next !== current, label: 'Different from the current password' },
		{ ok: next !== '' && next === repeat, label: 'Both new passwords match' }
	]);
	const valid = $derived(current !== '' && checks.every((c) => c.ok) && next.length <= 256);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		errors = {};
		formError = '';
		try {
			await api.post('/auth/change-password', {
				current_password: current,
				new_password: next
			});
			toast.success('Password changed', 'Your other sessions have been signed out.');
			current = next = repeat = '';
			await auth.load();
			await goto('/');
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				errors = err.details;
			} else {
				formError = errorMessage(err);
			}
		} finally {
			saving = false;
		}
	}
</script>

<PageHeader
	title="Change password"
	description="Choose a strong password you do not use anywhere else."
/>

<div class="max-w-xl space-y-4">
	{#if required}
		<Alert tone="warning" title="A new password is required">
			Your administrator requires you to set a new password before you can continue using Jenderal
			Cloud OS.
		</Alert>
	{/if}

	<Card>
		<form class="space-y-4" onsubmit={submit}>
			{#if formError}<Alert tone="danger">{formError}</Alert>{/if}
			<Field label="Current password" id="pw-current" error={errors.current_password} required>
				<input
					id="pw-current"
					type="password"
					class="input"
					autocomplete="current-password"
					bind:value={current}
					required
					aria-invalid={!!errors.current_password}
				/>
			</Field>
			<Field label="New password" id="pw-new" error={errors.new_password} required>
				<input
					id="pw-new"
					type="password"
					class="input"
					autocomplete="new-password"
					maxlength="256"
					bind:value={next}
					required
					aria-invalid={!!errors.new_password}
					aria-describedby="pw-rules"
				/>
			</Field>
			<Field label="Repeat new password" id="pw-repeat" required>
				<input
					id="pw-repeat"
					type="password"
					class="input"
					autocomplete="new-password"
					maxlength="256"
					bind:value={repeat}
					required
				/>
			</Field>

			<ul id="pw-rules" class="space-y-1 text-sm" aria-label="Password requirements">
				{#each checks as c (c.label)}
					<li class="flex items-center gap-2 {c.ok ? 'text-success' : 'text-fg-muted'}">
						{#if c.ok}<Check class="size-4" aria-hidden="true" />{:else}<Circle
								class="size-4"
								aria-hidden="true"
							/>{/if}
						<span>{c.label}<span class="sr-only">{c.ok ? ' (met)' : ' (not met)'}</span></span>
					</li>
				{/each}
			</ul>

			<p class="text-xs text-fg-muted">
				After the change, all your other sessions are signed out. This session stays active.
			</p>
			<div class="flex justify-end gap-2">
				{#if !required}<Button href="/account?tab=security">Cancel</Button>{/if}
				<Button type="submit" variant="primary" loading={saving} disabled={!valid}
					>Change password</Button
				>
			</div>
		</form>
	</Card>
	{#if required}
		<button type="button" class="link text-sm" onclick={() => auth.logout()}>Sign out</button>
	{/if}
</div>
