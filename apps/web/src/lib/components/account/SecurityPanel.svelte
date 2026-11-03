<script lang="ts">
	import { KeyRound, ShieldCheck, ShieldOff } from '@lucide/svelte';
	import { Alert, Badge, Button, Card, Field } from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';

	const me = $derived(auth.me);

	let password = $state('');
	let code = $state('');
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');

	async function disable(e: SubmitEvent) {
		e.preventDefault();
		const ok = await confirmDialog.confirm({
			title: 'Disable two-factor authentication?',
			message:
				'Your account will be protected by your password only. You can enable two-factor authentication again at any time.',
			confirmLabel: 'Disable',
			danger: true
		});
		if (!ok) return;
		saving = true;
		errors = {};
		formError = '';
		try {
			await api.post('/auth/mfa/disable', { password, code: code.trim() });
			password = '';
			code = '';
			await auth.load();
			toast.success('Two-factor authentication disabled');
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

<div class="grid gap-6 lg:grid-cols-2">
	<Card title="Password" description="Use a long, unique password.">
		<p class="text-sm text-fg-muted">Changing your password signs out all your other sessions.</p>
		<div class="mt-4">
			<Button href="/account/password"><KeyRound class="size-4" /> Change password</Button>
		</div>
	</Card>

	<Card title="Two-factor authentication">
		{#snippet actions()}
			{#if me?.mfa_enrolled}
				<Badge tone="success" dot>Enabled</Badge>
			{:else}
				<Badge tone="warning" dot>Not enabled</Badge>
			{/if}
		{/snippet}
		{#if !me}
			<p class="text-sm text-fg-muted">—</p>
		{:else if !me.mfa_enrolled}
			<p class="text-sm text-fg-muted">
				Protect your account with a time-based code from an authenticator app (TOTP).
				{#if me.mfa_required}Your role requires it.{/if}
			</p>
			<div class="mt-4">
				<Button variant="primary" href="/mfa/setup"
					><ShieldCheck class="size-4" /> Set up two-factor authentication</Button
				>
			</div>
		{:else if me.mfa_required}
			<Alert tone="info" title="Required by your role">
				Two-factor authentication is mandatory for your role and cannot be disabled. If you lost
				your authenticator, use a recovery code to sign in or ask an administrator to reset it.
			</Alert>
		{:else}
			<p class="text-sm text-fg-muted">
				Your account is protected with an authenticator app. To disable it, confirm your password
				and a current code.
			</p>
			<form class="mt-4 space-y-3" onsubmit={disable}>
				{#if formError}<Alert tone="danger">{formError}</Alert>{/if}
				<Field label="Current password" id="mfa-off-password" error={errors.password} required>
					<input
						id="mfa-off-password"
						type="password"
						class="input"
						autocomplete="current-password"
						bind:value={password}
						required
					/>
				</Field>
				<Field label="Authentication code" id="mfa-off-code" error={errors.code} required>
					<input
						id="mfa-off-code"
						class="input w-40 font-mono tracking-widest"
						inputmode="numeric"
						autocomplete="one-time-code"
						maxlength="6"
						bind:value={code}
						required
					/>
				</Field>
				<Button
					type="submit"
					variant="danger"
					loading={saving}
					disabled={!password || code.trim().length !== 6}
					><ShieldOff class="size-4" /> Disable two-factor authentication</Button
				>
			</form>
		{/if}
	</Card>
</div>
