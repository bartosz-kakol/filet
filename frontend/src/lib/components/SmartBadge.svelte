<script lang="ts">
	import { CircleDot, Minus, Moon, RefreshCw } from '@lucide/svelte';
	import { Popover } from 'bits-ui';
	import { ApiError, get, post } from '$lib/api/client';
	import type { Place, SmartStatus } from '$lib/api/types';
	import { formatBytes, formatDateTime, formatPowerOn, formatRelative } from '$lib/files/format';
	import { toasts } from '$lib/stores/toast.svelte';

	let { place, compact = false }: { place: Place; compact?: boolean } = $props();

	interface Detail {
		devices: {
			status: SmartStatus;
			polledAt: number | null;
			reason?: string;
			data: null | {
				device: string;
				protocol: string;
				model: string;
				serial: string;
				capacityBytes: number;
				temperatureC: number | null;
				powerOnHours: number | null;
				powerCycles: number | null;
				reallocatedSectors: number | null;
				pendingSectors: number | null;
				offlineUncorrectable: number | null;
				udmaCrcErrors: number | null;
				nvme: null | { percentageUsed: number; mediaErrors: number; availableSpare: number; criticalWarning: number };
				asleepAt: number | null;
			};
		}[];
	}

	const META: Record<SmartStatus, { label: string; cls: string }> = {
		ok: { label: 'Healthy', cls: 'text-ok' },
		warning: { label: 'Warning', cls: 'text-warn' },
		failing: { label: 'Failing', cls: 'text-danger' },
		standby: { label: 'Asleep', cls: 'text-muted' },
		unavailable: { label: 'SMART n/a', cls: 'text-muted' }
	};
	const status = $derived(place.smart?.status ?? 'unavailable');
	const reason = $derived(place.smart?.devices.find((d) => d.reason)?.reason ?? '');
	let open = $state(false);
	let detail = $state<Detail | null>(null);

	$effect(() => {
		if (open) void load();
	});

	async function load() {
		try {
			detail = await get<Detail>(`/api/places/${encodeURIComponent(place.id)}/smart`);
		} catch {
			detail = null;
		}
	}

	async function refresh() {
		try {
			await post(`/api/places/${encodeURIComponent(place.id)}/smart/refresh`);
			toasts.show('Checking drive health…');
			setTimeout(load, 4000);
		} catch (e) {
			toasts.error(e instanceof ApiError && e.code === 'rate_limited' ? 'Refreshed recently; try again in a minute' : 'Refresh failed');
		}
	}
</script>

{#if place.smart}
	<Popover.Root bind:open>
		<Popover.Trigger
			class="inline-flex items-center gap-1 rounded px-1 text-xs {META[status].cls} hover:bg-surface-2"
			aria-label="Drive health: {META[status].label}"
			title={status === 'unavailable' ? reason : META[status].label}
			onclick={(e: MouseEvent) => e.stopPropagation()}
		>
			{#if status === 'standby'}<Moon class="size-3" />{:else if status === 'unavailable'}<Minus class="size-3" />{:else}<CircleDot class="size-3 fill-current" />{/if}
			{#if !compact}<span>{META[status].label}</span>{/if}
		</Popover.Trigger>
		<Popover.Portal>
			<Popover.Content class="menu w-80 p-3" sideOffset={6}>
				<div class="mb-2 flex items-center justify-between">
					<span class="font-semibold">Drive health · {place.name}</span>
					<button class="icon-btn size-7" aria-label="Refresh" title="Refresh" onclick={refresh}><RefreshCw class="size-4" /></button>
				</div>
				{#if !detail}
					<p class="text-muted">Loading…</p>
				{:else}
					{#each detail.devices as d, i (i)}
						<div class="border-t border-line py-2 first:border-0">
							<div class="flex items-center justify-between">
								<span class="font-medium">{d.data?.model || d.data?.device || 'Drive'}</span>
								<span class={META[d.status].cls}>{META[d.status].label}</span>
							</div>
							{#if d.reason}<p class="text-xs text-muted">{d.reason}</p>{/if}
							{#if d.data && d.data.model}
								<dl class="mt-1 grid grid-cols-2 gap-x-2 text-xs">
									<dt class="text-muted">Device</dt><dd>{d.data.device} ({d.data.protocol})</dd>
									<dt class="text-muted">Serial</dt><dd class="truncate">{d.data.serial}</dd>
									<dt class="text-muted">Capacity</dt><dd>{formatBytes(d.data.capacityBytes)}</dd>
									{#if d.data.temperatureC !== null}<dt class="text-muted">Temperature</dt><dd>{d.data.temperatureC} °C</dd>{/if}
									<dt class="text-muted">Power-on</dt><dd>{formatPowerOn(d.data.powerOnHours)}</dd>
									{#if d.data.powerCycles !== null}<dt class="text-muted">Power cycles</dt><dd>{d.data.powerCycles}</dd>{/if}
									{#if d.data.reallocatedSectors !== null}<dt class="text-muted">Reallocated</dt><dd>{d.data.reallocatedSectors}</dd>{/if}
									{#if d.data.pendingSectors !== null}<dt class="text-muted">Pending</dt><dd>{d.data.pendingSectors}</dd>{/if}
									{#if d.data.offlineUncorrectable !== null}<dt class="text-muted">Uncorrectable</dt><dd>{d.data.offlineUncorrectable}</dd>{/if}
									{#if d.data.udmaCrcErrors !== null}<dt class="text-muted">CRC errors</dt><dd>{d.data.udmaCrcErrors}</dd>{/if}
									{#if d.data.nvme}
										<dt class="text-muted">Life used</dt><dd>{d.data.nvme.percentageUsed} %</dd>
										<dt class="text-muted">Spare</dt><dd>{d.data.nvme.availableSpare} %</dd>
										<dt class="text-muted">Media errors</dt><dd>{d.data.nvme.mediaErrors}</dd>
									{/if}
								</dl>
							{/if}
							<p class="mt-1 text-xs text-muted">
								Last checked {formatRelative(d.polledAt)}{#if d.data?.asleepAt} · drive asleep since {formatDateTime(d.data.asleepAt)}{/if}
							</p>
						</div>
					{/each}
				{/if}
			</Popover.Content>
		</Popover.Portal>
	</Popover.Root>
{/if}
