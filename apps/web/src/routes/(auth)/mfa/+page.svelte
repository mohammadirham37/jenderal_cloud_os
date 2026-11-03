<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { Alert, Button, Field } from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import type { Me } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';

	let code = $state('');
	let recovery = $state('');
	let useRecovery = $state(false);
	let loading = $state(false);
	let error = $state('');

	onMount(async () => {
		const me = await auth.load();
		if (!me) return goto('/login', { replace: true });
		if (!me.mfa_enrolled) return goto('/mfa/setup', { replace: true });
		if (me.mfa_verified) return goto(auth.gate() ?? '/', { replace: true });
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		loading = true;
		error = '';
		try {
			const body = useRecovery ? { recovery_code: recovery.trim() } : { code: code.trim() };
			const me = await api.post<Me>('/auth/mfa/verify', body, { noAuthRedirect: true });
			auth.set(me);
			await goto(auth.gate() ?? '/', { replace: true });
		} catch (err) {
			error =
				err instanceof ApiError && err.status === 422
					? 'The code is incorrect or was already used. Wait for the next code and try again.'
					: errorMessage(err);
			code = '';
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head><title>Two-factor authentication · ITK Cloud</title></svelte:head>

<h1 class="text-xl font-semibold">Two-factor authentication</h1>
<p class="mt-1 text-sm text-fg-muted">
	{useRecovery
		? 'Enter one of your recovery codes.'
		: 'Enter the 6-digit code from your authenticator app.'}
</p>
<form class="mt-6 space-y-4" onsubmit={submit}>
	{#if error}<Alert tone="danger">{error}</Alert>{/if}
	{#if useRecovery}
		<Field label="Recovery code" id="recovery" required>
			<input
				id="recovery"
				class="input font-mono uppercase"
				autocomplete="one-time-code"
				bind:value={recovery}
				placeholder="XXXXX-XXXXX"
			/>
		</Field>
	{:else}
		<Field label="Authentication code" id="code" required>
			<input
				id="code"
				class="input text-center font-mono text-lg tracking-[0.4em]"
				inputmode="numeric"
				autocomplete="one-time-code"
				maxlength="6"
				bind:value={code}
				placeholder="000000"
			/>
		</Field>
	{/if}
	<Button
		type="submit"
		variant="primary"
		class="w-full"
		{loading}
		disabled={useRecovery ? recovery.length < 10 : code.length !== 6}>Verify</Button
	>
	<div class="flex justify-between text-sm">
		<button type="button" class="link" onclick={() => (useRecovery = !useRecovery)}>
			{useRecovery ? 'Use authenticator code' : 'Use a recovery code'}
		</button>
		<button type="button" class="link" onclick={() => auth.logout()}>Sign out</button>
	</div>
</form>
