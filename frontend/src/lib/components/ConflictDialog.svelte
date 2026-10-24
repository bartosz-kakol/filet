<script lang="ts">
	import { Checkbox, Dialog } from 'bits-ui';
	import { Check, TriangleAlert } from '@lucide/svelte';
	import { formatBytes, formatDateTime } from '$lib/files/format';
	import { conflicts } from '$lib/stores/conflicts.svelte';
	import KindIcon from './KindIcon.svelte';
	import { kindOf } from '$lib/files/kinds';

	const cur = $derived(conflicts.current);
	const v = $derived(cur?.view);
	let applyAll = $state(false);
	let open = $state(false);

	$effect(() => {
		open = !!cur;
		applyAll = false;
	});

	function choose(action: 'overwrite' | 'rename' | 'skip') {
		conflicts.answer({ action, applyToAll: applyAll });
	}
</script>

<Dialog.Root
	{open}
	onOpenChange={(o) => {
		if (!o && cur) conflicts.answer(null);
	}}
>
	<Dialog.Portal>
		<Dialog.Overlay class="dialog-overlay" />
		{#if v}
			<Dialog.Content class="dialog">
				<Dialog.Title class="text-base font-semibold break-words">
					“{v.name}” already exists in {v.dir}
				</Dialog.Title>
				{#if v.uploading}
					<p class="flex items-center gap-2 text-sm text-warn">
						<TriangleAlert class="size-4 shrink-0" />Another upload to this name is in progress; overwriting cancels it
					</p>
				{/if}
				<div class="grid grid-cols-2 gap-3">
					{#each [{ label: 'Existing', d: v.existing }, { label: 'New', d: v.incoming }] as side (side.label)}
						<div class="flex flex-col gap-2 rounded-lg border border-line p-3 text-sm">
							<div class="text-xs font-medium tracking-wide text-muted uppercase">{side.label}</div>
							<div class="flex h-20 items-center justify-center overflow-hidden rounded bg-surface-2">
								{#if side.d?.previewUrl}
									<img src={side.d.previewUrl} alt="" class="h-full w-full object-cover" />
								{:else}
									<KindIcon
										kind={side.d && 'type' in side.d && side.d.type === 'dir'
											? 'dir'
											: side.d && 'isDir' in side.d && side.d.isDir
												? 'dir'
												: kindOf(v.name)}
										class="size-8"
									/>
								{/if}
							</div>
							<div>{side.d && 'type' in side.d && side.d.type === 'dir' ? 'Folder' : formatBytes(side.d?.size)}</div>
							<div class="text-muted">{formatDateTime(side.d?.mtime)}</div>
						</div>
					{/each}
				</div>
				<div class="flex flex-col gap-2">
					{#if !v.noOverwrite}
						<button class="btn h-auto flex-col items-start py-2 text-left" onclick={() => choose('overwrite')}>
							<span>Overwrite</span><span class="text-xs font-normal text-muted">Existing item goes to Trash</span>
						</button>
					{/if}
					<button class="btn btn-primary h-auto flex-col items-start py-2 text-left" onclick={() => choose('rename')}>
						<span>Keep both</span><span class="text-xs font-normal opacity-80">Saves as {v.suggestedName}</span>
					</button>
					<button class="btn" onclick={() => choose('skip')}>Skip</button>
				</div>
				{#if v.remaining !== 1}
					<label class="flex items-center gap-2 text-sm select-none">
						<Checkbox.Root
							bind:checked={applyAll}
							class="flex size-4 items-center justify-center rounded border border-line data-[state=checked]:border-accent data-[state=checked]:bg-accent"
						>
							{#snippet children({ checked })}
								{#if checked}<Check class="size-3 text-accent-fg" />{/if}
							{/snippet}
						</Checkbox.Root>
						Apply to all remaining conflicts{v.remaining > 1 ? ` (${v.remaining})` : ''}
					</label>
				{/if}
			</Dialog.Content>
		{/if}
	</Dialog.Portal>
</Dialog.Root>
