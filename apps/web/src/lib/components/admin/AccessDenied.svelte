<script lang="ts">
	import { Card, ErrorState } from '#lib/components/ui/index.ts';
	import { forbidden } from './access.ts';
</script>

<Card><ErrorState error={forbidden()} /></Card>
