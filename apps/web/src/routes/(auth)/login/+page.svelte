<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Alert, Button, Field } from '#lib/components/ui/index.ts';
	import { api, ApiError } from '#lib/api/client.ts';
	import type { Me } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';

	let username = $state('');
	let password = $state('');
	let loading = $state(false);
	let error = $state('');

	function safeNext(): string {
		const n = page.url.searchParams.get('next') ?? '/';
		// Only same-site relative paths (prevents open redirects).
		return n.startsWith('/') && !n.startsWith('//') ? n : '/';
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		loading = true;
		try {
			const me = await api.post<Me>(
				'/auth/login',
				{ username: username.trim(), password },
				{ noAuthRedirect: true }
			);
			auth.set(me);
			password = '';
			const gate = auth.gate();
			await goto(gate ?? safeNext(), { replace: true });
		} catch (err) {
			if (err instanceof ApiError) {
				error =
					err.code === 'rate_limited'
						? 'Too many attempts. Please wait a few minutes and try again.'
						: err.message;
			} else {
				error = 'Login failed.';
			}
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head><title>Sign in · ITK Cloud</title></svelte:head>

<h1 class="text-xl font-semibold">Sign in</h1>
<p class="mt-1 text-sm text-fg-muted">Use your laboratory account.</p>

<form class="mt-6 space-y-4" onsubmit={submit} novalidate>
	{#if error}<Alert tone="danger">{error}</Alert>{/if}
	<Field label="Username or email" id="username" required>
		<input id="username" class="input" autocomplete="username" bind:value={username} required />
	</Field>
	<Field label="Password" id="password" required>
		<input
			id="password"
			type="password"
			class="input"
			autocomplete="current-password"
			bind:value={password}
			required
		/>
	</Field>
	<div class="flex items-center justify-between">
		<a href="/forgot-password" class="link text-sm">Forgot password?</a>
	</div>
	<Button type="submit" variant="primary" class="w-full" {loading} disabled={!username || !password}
		>Sign in</Button
	>
</form>
