<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { Alert, Button, CopyButton, Field, Spinner } from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import type { Me } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';

	let secret = $state('');
	let uri = $state('');
	let code = $state('');
	let loading = $state(false);
	let error = $state('');
	let recoveryCodes = $state<string[]>([]);
	let acknowledged = $state(false);

	onMount(async () => {
		const me = await auth.load();
		if (!me) return goto('/login', { replace: true });
		if (me.mfa_enrolled) return goto(me.mfa_verified ? '/account' : '/mfa', { replace: true });
		try {
			const r = await api.post<{ secret: string; otpauth_uri: string }>(
				'/auth/mfa/setup',
				undefined,
				{ noAuthRedirect: true }
			);
			secret = r.secret;
			uri = r.otpauth_uri;
		} catch (err) {
			error = errorMessage(err);
		}
	});

	const grouped = $derived(secret.replace(/(.{4})/g, '$1 ').trim());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		loading = true;
		error = '';
		try {
			const r = await api.post<{ recovery_codes: string[]; me: Me }>(
				'/auth/mfa/enable',
				{ code: code.trim() },
				{ noAuthRedirect: true }
			);
			auth.set(r.me);
			recoveryCodes = r.recovery_codes;
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

	function download() {
		const blob = new Blob(
			[`ITK Cloud recovery codes for ${auth.me?.username}\n\n${recoveryCodes.join('\n')}\n`],
			{ type: 'text/plain' }
		);
		const a = document.createElement('a');
		a.href = URL.createObjectURL(blob);
		a.download = 'itk-cloud-recovery-codes.txt';
		a.click();
		URL.revokeObjectURL(a.href);
	}
</script>

<svelte:head><title>Set up two-factor authentication · ITK Cloud</title></svelte:head>

{#if recoveryCodes.length}
	<h1 class="text-xl font-semibold">Save your recovery codes</h1>
	<p class="mt-1 text-sm text-fg-muted">
		Each code can be used once if you lose access to your authenticator. They will not be shown
		again.
	</p>
	<ul
		class="mt-4 grid grid-cols-2 gap-2 rounded-md border border-border bg-panel-2 p-4 font-mono text-sm"
	>
		{#each recoveryCodes as c (c)}<li>{c}</li>{/each}
	</ul>
	<div class="mt-3 flex gap-2">
		<Button size="sm" onclick={download}>Download</Button>
		<CopyButton text={recoveryCodes.join('\n')} label="Copy recovery codes" />
	</div>
	<label class="mt-4 flex items-center gap-2 text-sm">
		<input
			type="checkbox"
			class="rounded border-border-strong text-accent"
			bind:checked={acknowledged}
		/> I have stored these codes safely.
	</label>
	<Button
		variant="primary"
		class="mt-4 w-full"
		disabled={!acknowledged}
		onclick={() => goto(auth.gate() ?? '/', { replace: true })}>Continue</Button
	>
{:else}
	<h1 class="text-xl font-semibold">Set up two-factor authentication</h1>
	<p class="mt-1 text-sm text-fg-muted">
		{auth.me?.mfa_required
			? 'Your role requires two-factor authentication.'
			: 'Protect your account with an authenticator app.'}
	</p>
	{#if error}<div class="mt-4"><Alert tone="danger">{error}</Alert></div>{/if}
	{#if !secret && !error}
		<div class="flex justify-center py-8"><Spinner /></div>
	{:else if secret}
		<ol class="mt-5 list-decimal space-y-3 pl-5 text-sm text-fg-muted">
			<li>Open an authenticator app (e.g. Google Authenticator, Aegis, 1Password).</li>
			<li>
				Add an account using this setup key:
				<div
					class="mt-2 flex items-center gap-2 rounded-md border border-border bg-panel-2 px-3 py-2 font-mono text-sm text-fg"
				>
					<span class="flex-1 break-all">{grouped}</span><CopyButton
						text={secret}
						label="Copy setup key"
					/>
				</div>
				<p class="mt-1 text-xs">Type: time-based (TOTP), 6 digits, 30 seconds.</p>
				<details class="mt-2 text-xs">
					<summary class="cursor-pointer">Show setup URI</summary>
					<code class="mt-1 block break-all">{uri}</code>
				</details>
			</li>
			<li>Enter the 6-digit code shown by the app.</li>
		</ol>
		<form class="mt-5 space-y-4" onsubmit={submit}>
			<Field label="Authentication code" id="code" required>
				<input
					id="code"
					class="input text-center font-mono text-lg tracking-[0.4em]"
					inputmode="numeric"
					maxlength="6"
					autocomplete="one-time-code"
					bind:value={code}
					placeholder="000000"
				/>
			</Field>
			<Button type="submit" variant="primary" class="w-full" {loading} disabled={code.length !== 6}
				>Enable two-factor authentication</Button
			>
		</form>
	{/if}
	<button type="button" class="link mt-4 text-sm" onclick={() => auth.logout()}>Sign out</button>
{/if}
