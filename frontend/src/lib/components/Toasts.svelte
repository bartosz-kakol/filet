<script lang="ts">
	import { X } from '@lucide/svelte';
	import { flip } from 'svelte/animate';
	import { mfly, reducedMotion } from '$lib/motion';
	import { toasts } from '$lib/stores/toast.svelte';
</script>

<!-- [M10] toasts drop in from the top, slide away up, neighbours glide into place; non-sticky ones show a countdown bar -->
<div class="pointer-events-none fixed top-3 left-1/2 z-[60] flex w-[min(28rem,calc(100vw-2rem))] -translate-x-1/2 flex-col gap-2" aria-live="polite">
	{#each toasts.list as t (t.id)}
		<div
			class="pointer-events-auto relative flex items-center gap-3 overflow-hidden rounded-lg border px-3 py-2 text-sm shadow-lg {t.kind === 'error'
				? 'border-danger/40 bg-bg text-danger'
				: 'border-line bg-bg text-fg'}"
			role={t.kind === 'error' ? 'alert' : 'status'}
			in:mfly={{ y: -24, duration: 320 }}
			out:mfly={{ y: -12, duration: 180 }}
			animate:flip={{ duration: reducedMotion() ? 0 : 220 }}
		>
			<span class="flex-1">{t.text}</span>
			{#if t.action}
				<button class="btn h-7 px-2" onclick={() => { t.action!.run(); toasts.dismiss(t.id); }}>{t.action.label}</button>
			{/if}
			<button class="icon-btn size-7" aria-label="Dismiss" onclick={() => toasts.dismiss(t.id)}><X class="size-4" /></button>
			{#if t.ms}
				<span class="m-toast-timer absolute inset-x-0 bottom-0 h-0.5 bg-current opacity-30" style:animation-duration="{t.ms}ms" aria-hidden="true"></span>
			{/if}
		</div>
	{/each}
</div>
