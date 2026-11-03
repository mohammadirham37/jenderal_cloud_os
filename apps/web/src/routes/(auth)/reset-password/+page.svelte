<script lang="ts">
	import { onMount } from 'svelte';
	import { Alert, Button, Field } from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';

	// The token is carried in the URL fragment so it is never sent to the
	// server in request logs or Referer headers.
	let token = $state('');
	let password = $state('');
	let confirm = $state('');
	let loading = $state(false);
	let done = $state(false);
	let error = $state('');
	let fieldError = $state('');

	onMount(() => {
		const m = location.hash.match(/token=([A-Za-z0-9_-]+)/);
		token = m ? m[1] : '';
		history.replaceState(null, '', location.pathname);
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = fieldError = '';
		if (password !== confirm) {
			fieldError = 'Passwords do not match.';
			return;
		}
		loading = true;
		try {
			await api.post('/auth/reset-password', { token, password }, { noAuthRedirect: true });
			done = true;
		} catch (err) {
			if (err instanceof ApiError && err.details.password)
				fieldError = `Password ${err.details.password}.`;
			else error = errorMessage(err);
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head><title>Choose a new password · Jenderal Cloud OS</title></svelte:head>

<h1 class="text-xl font-semibold">Choose a new password</h1>
{#if done}
	<div class="mt-6 space-y-4">
		<Alert tone="success" title="Password updated"
			>All existing sessions were signed out. You can now sign in with the new password.</Alert
		>
		<a href="/login" class="link text-sm">Go to sign in</a>
	</div>
{:else if !token}
	<div class="mt-6">
		<Alert tone="danger">This reset link is invalid. Request a new one.</Alert>
	</div>
{:else}
	<form class="mt-6 space-y-4" onsubmit={submit}>
		{#if error}<Alert tone="danger">{error}</Alert>{/if}
		<Field
			label="New password"
			id="password"
			hint="At least 12 characters with three of: lowercase, uppercase, digits, symbols."
			required
			error={fieldError}
		>
			<input
				id="password"
				type="password"
				class="input"
				autocomplete="new-password"
				bind:value={password}
				required
			/>
		</Field>
		<Field label="Repeat password" id="confirm" required>
			<input
				id="confirm"
				type="password"
				class="input"
				autocomplete="new-password"
				bind:value={confirm}
				required
			/>
		</Field>
		<Button
			type="submit"
			variant="primary"
			class="w-full"
			{loading}
			disabled={!password || !confirm}>Update password</Button
		>
	</form>
{/if}
