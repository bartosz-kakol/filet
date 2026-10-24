<script lang="ts">
	import { ChevronUp, Minus, Plus, Settings, X } from '@lucide/svelte';
	import { Popover } from 'bits-ui';
	import { createVirtualizer } from '@tanstack/svelte-virtual';
	import { untrack } from 'svelte';
	import { formatEta, formatSpeed } from '$lib/files/format';
	import { jobs, TERMINAL_JOB } from '$lib/jobs/jobs.svelte';
	import { mfly, mslide } from '$lib/motion';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import { uploads } from '$lib/upload/uploads.svelte';
	import JobRow from './JobRow.svelte';
	import UploadRow from './UploadRow.svelte';

	let hidden = $state(false);
	let onlyFailed = $state(false);
	let lastCount = 0;

	const ups = $derived(uploads.items);
	const failedStates = new Set(['failed', 'corrupted', 'removed']);
	const visibleUploads = $derived(onlyFailed ? ups.filter((u) => failedStates.has(u.state)) : ups);
	const failedCount = $derived(ups.filter((u) => failedStates.has(u.state)).length + jobs.list.filter((j) => j.status === 'failed').length);
	const activeUploads = $derived(ups.filter((u) => !u.terminal));
	const activeJobs = $derived(jobs.list.filter((j) => !TERMINAL_JOB.has(j.status)));
	const anyActive = $derived(activeUploads.length > 0 || activeJobs.length > 0);
	const show = $derived(!hidden && (ups.length > 0 || jobs.list.length > 0));

	// The floating selection bar reads this to stay above the panel.
	let panelH = $state(0);
	$effect(() => {
		if (!show) return;
		document.documentElement.style.setProperty('--transfer-h', `calc(${panelH}px + 0.5rem)`);
		return () => document.documentElement.style.removeProperty('--transfer-h');
	});

	// A new batch expands the panel.
	$effect(() => {
		const n = ups.length + jobs.list.length;
		if (n > lastCount) {
			hidden = false;
			prefs.panelCollapsed = false;
		}
		lastCount = n;
	});

	const summary = $derived.by(() => {
		if (activeUploads.some((u) => u.state === 'waiting-network')) return 'Waiting for connection…';
		if (activeUploads.some((u) => u.state === 'paused-auth')) return 'Sign in to continue';
		const parts: string[] = [];
		if (activeUploads.length) {
			const done = ups.filter((u) => u.state === 'done').length;
			const total = ups.filter((u) => u.state !== 'skipped' && u.state !== 'cancelled').length;
			const speed = activeUploads.reduce((s, u) => s + u.speed, 0);
			const left = activeUploads.reduce((s, u) => s + Math.max(0, u.size - u.sentBytes), 0);
			let s = `Uploading ${Math.min(done + 1, total)} of ${total}`;
			if (speed > 0) s += ` · ${formatSpeed(speed)}`;
			const eta = speed > 0 ? formatEta(left / speed) : '';
			if (eta) s += ` · ${eta.replace(' left', '')} left`;
			parts.push(s);
		}
		if (activeJobs.length) {
			const j = activeJobs[0];
			const verb = { copy: 'Copying', move: 'Moving', trash: 'Deleting', restore: 'Restoring', purge: 'Purging' }[j.type];
			parts.push(activeJobs.length > 1 ? `${activeJobs.length} operations` : `${verb} ${j.itemsTotal || ''} items`.replace('  ', ' '));
		}
		return parts.length ? parts.join(' · ') : 'All done';
	});

	let scrollEl: HTMLDivElement | undefined = $state();
	const virtual = createVirtualizer<HTMLDivElement, HTMLDivElement>({
		count: 0,
		getScrollElement: () => scrollEl ?? null,
		estimateSize: () => 76,
		overscan: 6
	});
	const useVirtual = $derived(visibleUploads.length > 50);
	$effect(() => {
		const count = useVirtual ? visibleUploads.length : 0;
		untrack(() => $virtual.setOptions({ count }));
	});

	function close() {
		uploads.clearFinished();
		jobs.clearFinished();
		hidden = true;
	}

	function clearFinished() {
		uploads.clearFinished();
		jobs.clearFinished();
		onlyFailed = false;
	}
</script>

{#if show}
	<!-- [M28] panel flies up in and down out; body slides open/closed; rows slide in/out -->
	<section
		in:mfly|global={{ y: 60, duration: 320 }}
		out:mfly={{ y: 60, duration: 200 }}
		bind:offsetHeight={panelH}
		class="fixed right-0 bottom-0 z-40 flex w-full flex-col overflow-hidden border border-line bg-bg shadow-2xl md:right-4 md:bottom-4 md:w-96 md:rounded-xl max-md:rounded-t-2xl"
		aria-label="Transfers"
		data-testid="transfer-panel"
	>
		<header class="flex h-14 items-center gap-1 border-b border-line pr-1 pl-4 md:h-12">
			<button class="min-w-0 flex-1 truncate text-left text-sm font-medium" onclick={() => (prefs.panelCollapsed = !prefs.panelCollapsed)} data-testid="panel-summary">
				{summary}
			</button>
			<Popover.Root>
				<Popover.Trigger class="icon-btn size-8" aria-label="Upload settings" title="Upload settings"><Settings class="m-gear size-4" /></Popover.Trigger><!-- [M27] -->
				<Popover.Portal>
					<Popover.Content class="menu z-50 p-3" sideOffset={6}>
						<div class="mb-2 text-sm font-medium">Parallel uploads</div>
						<div class="flex items-center gap-2">
							<button class="icon-btn size-8 border border-line" aria-label="Fewer parallel uploads" disabled={prefs.uploadConcurrency <= 1} onclick={() => { prefs.uploadConcurrency--; uploads.concurrencyChanged(); }}><Minus class="size-4" /></button>
							<span class="w-8 text-center tabular-nums" data-testid="concurrency">{prefs.uploadConcurrency}</span>
							<button class="icon-btn size-8 border border-line" aria-label="More parallel uploads" disabled={prefs.uploadConcurrency >= 16} onclick={() => { prefs.uploadConcurrency++; uploads.concurrencyChanged(); }}><Plus class="size-4" /></button>
						</div>
					</Popover.Content>
				</Popover.Portal>
			</Popover.Root>
			<button class="icon-btn size-8" aria-label={prefs.panelCollapsed ? 'Expand' : 'Collapse'} onclick={() => (prefs.panelCollapsed = !prefs.panelCollapsed)}>
				<ChevronUp class="size-4 transition-transform duration-300 {prefs.panelCollapsed ? '' : 'rotate-180'}" /><!-- [M28] -->
			</button>
			{#if !anyActive}
				<button class="icon-btn size-8" aria-label="Close" onclick={close}><X class="size-4" /></button>
			{/if}
		</header>
		{#if !prefs.panelCollapsed}
			<div transition:mslide={{ duration: 260 }}>
				<div class="max-h-[50vh] overflow-auto" bind:this={scrollEl}>
					{#if visibleUploads.length}
						<h3 class="sticky top-0 z-10 bg-bg px-4 pt-2 pb-1 text-xs font-medium tracking-wide text-muted uppercase">Uploads</h3>
						{#if useVirtual}
							<div class="relative" style:height="{$virtual.getTotalSize()}px">
								{#each $virtual.getVirtualItems().filter((v) => v.index < visibleUploads.length) as row (row.key)}
									<div class="absolute inset-x-0" style:transform="translateY({row.start}px)" data-index={row.index} use:$virtual.measureElement>
										<UploadRow item={visibleUploads[row.index]} />
									</div>
								{/each}
							</div>
						{:else}
							{#each visibleUploads as item (item.key)}
								<div transition:mslide={{ duration: 220 }}><UploadRow {item} /></div>
							{/each}
						{/if}
					{/if}
					{#if jobs.list.length && !onlyFailed}
						<h3 class="sticky top-0 z-10 bg-bg px-4 pt-2 pb-1 text-xs font-medium tracking-wide text-muted uppercase">Operations</h3>
						{#each jobs.list as job (job.id)}
							<div transition:mslide={{ duration: 220 }}><JobRow {job} /></div>
						{/each}
					{/if}
				</div>
				<footer class="flex items-center justify-between border-t border-line px-3 py-1.5 text-xs">
					<button class="rounded px-2 py-1 text-muted hover:bg-surface-2 hover:text-fg" onclick={clearFinished}>Clear finished</button>
					{#if failedCount}
						<button class="rounded px-2 py-1 text-danger hover:bg-surface-2 {onlyFailed ? 'bg-surface-2' : ''}" onclick={() => (onlyFailed = !onlyFailed)}>
							{failedCount} failed
						</button>
					{/if}
				</footer>
			</div>
		{/if}
	</section>
{/if}
