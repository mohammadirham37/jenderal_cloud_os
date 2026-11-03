<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { PageHeader, Tabs } from '#lib/components/ui/index.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import HostOverview from '#lib/components/monitoring/HostOverview.svelte';
	import HostHistory from '#lib/components/monitoring/HostHistory.svelte';
	import GpuPanel from '#lib/components/monitoring/GpuPanel.svelte';
	import ServicesPanel from '#lib/components/monitoring/ServicesPanel.svelte';
	import AlertsPanel from '#lib/components/monitoring/AlertsPanel.svelte';
	import JobsPanel from '#lib/components/monitoring/JobsPanel.svelte';

	const tabs = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'history', label: 'History' },
		{ id: 'gpu', label: 'GPU' },
		{ id: 'services', label: 'Services' },
		{ id: 'alerts', label: 'Alerts' },
		{ id: 'jobs', label: 'Jobs' }
	];
	const valid = (t: string | null) => (t && tabs.some((x) => x.id === t) ? t : 'overview');
	// Each panel is mounted only while its tab is active, so only the visible
	// tab polls the API.
	let active = $derived(valid(page.url.searchParams.get('tab')));

	function select(id: string) {
		goto(`${page.url.pathname}?tab=${id}`, { replace: true, reset: false });
	}
	const allowed = $derived(auth.can('monitoring.view_host'));
</script>

<PageHeader
	title="Monitoring"
	description="Live host metrics, history, GPUs, platform services, alerts and background jobs."
/>

{#if !allowed}
	<AccessDenied />
{:else}
	<Tabs {tabs} bind:active onchange={select} />
	<div class="mt-6" role="tabpanel" aria-labelledby="tab-{active}">
		{#if active === 'overview'}
			<HostOverview />
		{:else if active === 'history'}
			<HostHistory />
		{:else if active === 'gpu'}
			<GpuPanel />
		{:else if active === 'services'}
			<ServicesPanel />
		{:else if active === 'alerts'}
			<AlertsPanel />
		{:else}
			<JobsPanel />
		{/if}
	</div>
{/if}
