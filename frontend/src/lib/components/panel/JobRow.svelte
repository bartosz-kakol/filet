<script lang="ts">
	import { ArchiveRestore, ChevronDown, Copy, FolderInput, Trash2, X } from '@lucide/svelte';
	import type { Job } from '$lib/api/types';
	import { formatBytes } from '$lib/files/format';
	import { baseName } from '$lib/files/path';
	import { jobs, TERMINAL_JOB } from '$lib/jobs/jobs.svelte';
	import { mslide } from '$lib/motion';
	import { places } from '$lib/stores/places.svelte';

	let { job }: { job: Job } = $props();
	let showErrors = $state(false);

	const count = $derived(job.spec.src?.paths.length ?? job.spec.paths?.length ?? job.spec.ids?.length ?? job.itemsTotal);
	const items = $derived(`${count} item${count === 1 ? '' : 's'}`);
	const title = $derived.by(() => {
		const dst = job.spec.dst ? `${places.name(job.spec.dst.place)}${job.spec.dst.dir === '/' ? '' : job.spec.dst.dir}` : '';
		switch (job.type) {
			case 'copy':
				return `Copying ${items} to ${dst}`;
			case 'move':
				return `Moving ${items} to ${dst}`;
			case 'trash':
				return `Moving ${items} to Trash`;
			case 'restore':
				return `Restoring ${items}`;
			case 'purge':
				return job.spec.all ? 'Emptying trash' : `Deleting ${items} permanently`;
		}
		return job.type;
	});
	const terminal = $derived(TERMINAL_JOB.has(job.status));
	const status = $derived.by(() => {
		const errs = job.result.errors.length;
		switch (job.status) {
			case 'queued':
				return 'Queued';
			case 'running':
			case 'awaiting_input': {
				const bytes = job.bytesTotal ? ` · ${formatBytes(job.bytesDone)} of ${formatBytes(job.bytesTotal)}` : '';
				return `${job.itemsDone} of ${job.itemsTotal} items${bytes}`;
			}
			case 'completed':
				return errs ? `Completed with ${errs} error${errs === 1 ? '' : 's'}` : 'Done';
			case 'cancelled':
				return `Cancelled — ${job.itemsDone} of ${job.itemsTotal} items done`;
			case 'failed':
				return `Failed: ${job.error?.message ?? 'error'}`;
			case 'interrupted':
				return 'Interrupted by server restart';
		}
		return job.status;
	});
	const progress = $derived(
		job.bytesTotal > 0 ? job.bytesDone / job.bytesTotal : job.itemsTotal > 0 ? job.itemsDone / job.itemsTotal : 0
	);
	const Icon = $derived(job.type === 'copy' ? Copy : job.type === 'move' ? FolderInput : job.type === 'restore' ? ArchiveRestore : Trash2);
</script>

<div class="flex gap-3 px-3 py-2" data-testid="job-row" data-status={job.status}>
	<Icon class="mt-0.5 size-5 shrink-0 text-muted" />
	<div class="min-w-0 flex-1">
		<div class="truncate text-sm font-medium" {title}>{title}</div>
		<div class="truncate text-xs {job.status === 'failed' ? 'text-danger' : 'text-muted'}">{status}</div>
		{#if job.currentItem && !terminal}
			<div class="truncate text-xs text-muted" title={job.currentItem}>{baseName(job.currentItem)}</div>
		{/if}
		{#if job.status === 'awaiting_input'}
			<button class="btn btn-primary mt-1 h-7 px-2 text-xs" onclick={() => jobs.prompt(job)}>Needs your decision</button>
		{/if}
		{#if !terminal}
			<div class="mt-1 h-1 overflow-hidden rounded bg-surface-2">
				<div class="m-stripes h-full bg-accent transition-[width] duration-300" style:width="{progress * 100}%"></div><!-- [M29] -->
			</div>
		{/if}
		{#if terminal && job.result.errors.length}
			<button class="mt-1 flex items-center gap-1 text-xs text-muted hover:text-fg" onclick={() => (showErrors = !showErrors)}>
				<ChevronDown class="size-3 transition-transform duration-200 {showErrors ? 'rotate-180' : ''}" />{showErrors ? 'Hide' : 'Show'} errors
			</button>
			{#if showErrors}
				<ul transition:mslide={{ duration: 200 }} class="mt-1 max-h-32 overflow-auto text-xs">
					{#each job.result.errors as e (e.path + e.code)}
						<li class="truncate" title={e.path}><span class="text-danger">{e.code}</span> {e.path}</li>
					{/each}
				</ul>
			{/if}
		{/if}
	</div>
	<div class="flex shrink-0 items-start">
		{#if !terminal}
			<button class="icon-btn size-8" aria-label="Cancel operation" title="Cancel" onclick={() => jobs.cancel(job.id)}><X class="size-4" /></button>
		{:else}
			<button class="icon-btn size-8" aria-label="Dismiss" title="Dismiss" onclick={() => jobs.dismiss(job.id)}><X class="size-4" /></button>
		{/if}
	</div>
</div>
