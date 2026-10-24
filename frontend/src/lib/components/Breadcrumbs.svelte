<script lang="ts">
	import { ChevronRight, Ellipsis } from '@lucide/svelte';
	import { DropdownMenu } from 'bits-ui';
	import { goto } from '$app/navigation';
	import { mfly } from '$lib/motion';
	import { segments } from '$lib/files/path';
	import { viewport } from '$lib/stores/media.svelte';

	interface Crumb {
		label: string;
		path: string;
	}

	let {
		rootLabel,
		path,
		href,
		dropTarget
	}: {
		rootLabel: string;
		path: string;
		href: (dir: string) => string;
		/** Returns drag/drop handlers for a directory (or nothing). */
		dropTarget?: (dir: string) => Record<string, (e: DragEvent) => void>;
	} = $props();

	const crumbs = $derived.by(() => {
		const out: Crumb[] = [{ label: rootLabel, path: '/' }];
		let acc = '';
		for (const s of segments(path)) {
			acc += '/' + s;
			out.push({ label: s, path: acc });
		}
		return out;
	});
	const collapse = $derived(!viewport.mobile && crumbs.length > 5);
	const visible = $derived(collapse ? [crumbs[0], crumbs[1], ...crumbs.slice(-2)] : crumbs);
	const hidden = $derived(collapse ? crumbs.slice(2, -2) : []);
	let scroller: HTMLElement | undefined = $state();
	$effect(() => {
		void path;
		if (scroller) scroller.scrollLeft = scroller.scrollWidth;
	});
</script>

<nav aria-label="Breadcrumbs" class="min-w-0 flex-1 overflow-x-auto [scrollbar-width:none]" bind:this={scroller}>
	<ol class="flex items-center text-sm whitespace-nowrap">
		{#each visible as c, i (c.path)}
			{#if i > 0}<li aria-hidden="true"><ChevronRight class="size-4 text-muted" /></li>{/if}
			{#if collapse && i === 2}
				<li>
					<DropdownMenu.Root>
						<DropdownMenu.Trigger class="icon-btn size-7" aria-label="Show hidden folders"><Ellipsis class="m-rot90 size-4" /></DropdownMenu.Trigger>
						<DropdownMenu.Portal>
							<DropdownMenu.Content class="menu" sideOffset={4}>
								{#each hidden as h (h.path)}
									<DropdownMenu.Item class="menu-item" onSelect={() => goto(href(h.path))}>{h.label}</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Portal>
					</DropdownMenu.Root>
				</li>
				<li aria-hidden="true"><ChevronRight class="size-4 text-muted" /></li>
			{/if}
			<!-- [M19] a deeper crumb slides in; [M48] m-underline -->
			<li in:mfly={{ x: -10, duration: 240 }}>
				<a
					href={href(c.path)}
					class="m-underline block max-w-48 truncate rounded px-1.5 py-1 hover:bg-surface-2 data-[drop=true]:bg-accent/15 data-[drop=true]:outline-2 data-[drop=true]:outline-accent {i ===
					visible.length - 1
						? 'font-semibold'
						: 'text-muted'}"
					aria-current={i === visible.length - 1 ? 'page' : undefined}
					{...dropTarget?.(c.path) ?? {}}>{c.label}</a
				>
			</li>
		{/each}
	</ol>
</nav>
