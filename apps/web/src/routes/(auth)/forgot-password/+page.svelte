<script lang="ts">
	import { Alert, Button, Field } from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';

	let email = $state('');
	let loading = $state(false);
	let sent = $state(false);
	let emailDelivery = $state(true);
	let error = $state('');

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		loading = true;
		error = '';
		try {
			const r = await api.post<{ email_delivery: boolean }>(
				'/auth/forgot-password',
				{ email: email.trim() },
				{ noAuthRedirect: true }
			);
			emailDelivery = r.email_delivery;
			sent = true;
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head><title>Forgot password · Jenderal Cloud OS</title></svelte:head>

<h1 class="text-xl font-semibold">Reset your password</h1>
{#if sent}
	<div class="mt-6 space-y-4">
		{#if emailDelivery}
			<Alert tone="success" title="Check your inbox"
				>If an active account uses this address, a reset link valid for one hour has been sent.</Alert
			>
		{:else}
			<Alert tone="warning" title="Email is not configured on this server">
				Ask a laboratory administrator to issue a password reset link for your account.
			</Alert>
		{/if}
		<a href="/login" class="link text-sm">Back to sign in</a>
	</div>
{:else}
	<p class="mt-1 text-sm text-fg-muted">Enter the email address of your account.</p>
	<form class="mt-6 space-y-4" onsubmit={submit}>
		{#if error}<Alert tone="danger">{error}</Alert>{/if}
		<Field label="Email" id="email" required>
			<input
				id="email"
				type="email"
				class="input"
				autocomplete="email"
				bind:value={email}
				required
			/>
		</Field>
		<Button type="submit" variant="primary" class="w-full" {loading} disabled={!email.includes('@')}
			>Send reset link</Button
		>
		<a href="/login" class="link block text-center text-sm">Back to sign in</a>
	</form>
{/if}
