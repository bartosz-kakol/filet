<script lang="ts">
	import {
		ArrowUpDown,
		Check,
		FolderPlus,
		FolderUp,
		LayoutGrid,
		List,
		Lock,
		Plus,
		RefreshCw,
		Search,
		Upload,
		X,
		ChevronDown,
		FolderOpen,
		FolderX
	} from '@lucide/svelte';
	import { Dialog, DropdownMenu, ToggleGroup } from 'bits-ui';
	import { onMount, untrack } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { ApiError, errorMessage, post } from '$lib/api/client';
	import { events } from '$lib/api/events';
	import type { Source } from '$lib/api/source';
	import type { Entry, Listing, Place } from '$lib/api/types';
	import {
		FILET_DRAG,
		canOpen,
		downloadSelection,
		planUpload,
		type DragPayload
	} from '$lib/files/actions';
	import { listingCache } from '$lib/files/listing.svelte';
	import { isWithin, joinPath, parentPath } from '$lib/files/path';
	import { filterHidden, sortEntries } from '$lib/files/sort';
	import { jobs } from '$lib/jobs/jobs.svelte';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import type { SortKey } from '$lib/prefs/storage';
	import { getViewMode, setViewMode, type ViewMode } from '$lib/prefs/viewmodes';
	import { viewport } from '$lib/stores/media.svelte';
	import { toasts } from '$lib/stores/toast.svelte';
	import { entriesFromDrop, fromDataTransferFiles, fromFileList, readEntries, type Picked } from '$lib/upload/sources';
	import { uploads } from '$lib/upload/uploads.svelte';
	import AppMenu from '../AppMenu.svelte';
	import Breadcrumbs from '../Breadcrumbs.svelte';
	import ConfirmDialog from '../ConfirmDialog.svelte';
	import NameDialog from '../NameDialog.svelte';
	import PlaceChooser from '../PlaceChooser.svelte';
	import FileGrid from './FileGrid.svelte';
	import FileList from './FileList.svelte';
	import ItemMenu, { type MenuAction } from './ItemMenu.svelte';
	import SearchResults from './SearchResults.svelte';
	import SelectionBar from './SelectionBar.svelte';
	import type { ItemCtl, Placeholder } from './types';
	import type { Snippet } from 'svelte';

	let {
		src,
		place,
		path,
		rootLabel,
		hrefFor,
		q = '',
		previewName = null,
		publicHeader
	}: {
		src: Source;
		place: Place | null;
		path: string;
		rootLabel: string;
		hrefFor: (dir: string, query?: Record<string, string>) => string;
		q?: string;
		previewName?: string | null;
		publicHeader?: Snippet;
	} = $props();

	const isPublic = $derived(src.isPublic);
	const placeId = $derived(place?.id ?? '');
	const readonly = $derived(isPublic || !!place?.readonly);

	// --- listing ---
	let listing = $state<Listing | null>(null);
	let loadError = $state<ApiError | null>(null);
	let loaded = $state(false);
	let lastFetch = 0;
	let reqKey = '';

	async function load(navigated = false) {
		const key = src.base + ':' + path;
		reqKey = key;
		if (navigated) {
			const cached = listingCache.peek(src, path);
			listing = cached?.listing ?? null;
			loaded = !!cached;
			loadError = null;
		}
		try {
			const l = await listingCache.fetch(src, path);
			if (reqKey !== key) return;
			listing = l;
			loadError = null;
			lastFetch = Date.now();
		} catch (e) {
			if (reqKey !== key) return;
			loadError = e instanceof ApiError ? e : new ApiError(0, 'network', errorMessage(e));
			if (loadError.status === 404 || loadError.status === 403 || loadError.status === 400) listing = null;
		} finally {
			if (reqKey === key) loaded = true;
		}
	}

	// [M18] Entries stagger in once per folder open or view switch (not on background refreshes).
	let fresh = $state(false);
	let freshPending = true;
	let freshTimer: ReturnType<typeof setTimeout> | null = null;
	function markFresh() {
		fresh = true;
		if (freshTimer) clearTimeout(freshTimer);
		freshTimer = setTimeout(() => (fresh = false), 700);
	}
	$effect(() => {
		if (listing && freshPending) {
			freshPending = false;
			untrack(markFresh);
		}
	});

	$effect(() => {
		void src.base;
		void path;
		untrack(() => {
			freshPending = true;
			selection.clear();
			focused = -1;
			anchor = -1;
			selectionMode = false;
			void load(true);
			viewMode = getViewMode(viewKey, path);
		});
	});

	let fsTimer: ReturnType<typeof setTimeout> | null = null;
	onMount(() => {
		const offs = [
			events.on('fs.changed', (d: { place: string; dir: string }) => {
				if (isPublic || d.place !== placeId || d.dir !== path) return;
				if (fsTimer) return;
				fsTimer = setTimeout(() => {
					fsTimer = null;
					void load();
				}, 150);
			}),
			events.on('upload.progress', (d: { place: string; dir: string; id: string; receivedBytes: number }) => {
				if (!listing || d.place !== placeId || d.dir !== path) return;
				const u = listing.uploads.find((x) => x.id === d.id);
				if (u) u.receivedBytes = d.receivedBytes;
			}),
			events.onReconnect(() => void load())
		];
		const onFocus = () => {
			if (Date.now() - lastFetch > 5000) void load();
		};
		window.addEventListener('focus', onFocus);
		return () => {
			offs.forEach((f) => f());
			window.removeEventListener('focus', onFocus);
		};
	});

	const entries = $derived(sortEntries(filterHidden(listing?.entries ?? [], isPublic ? false : prefs.showHidden), prefs.sort));

	const placeholders = $derived.by((): Placeholder[] => {
		if (isPublic || !listing) return [];
		const out = new Map<string, Placeholder>();
		for (const u of listing.uploads) {
			out.set(u.name, { key: u.id, name: u.name, size: u.size, progress: u.size ? u.receivedBytes / u.size : 0, leftover: !u.active });
		}
		for (const it of uploads.items) {
			if (it.place !== placeId || it.dir !== path || it.terminal || !it.serverId) continue;
			out.set(it.name, { key: it.key, name: it.name, size: it.size, progress: it.progress, leftover: false });
		}
		const names = new Set(entries.map((e) => e.name));
		return [...out.values()].filter((p) => !names.has(p.name));
	});

	const previewSeq = $derived(entries.filter((e) => e.type !== 'dir' && !e.badName));
	const previewEntry = $derived(previewName ? previewSeq.find((e) => e.name === previewName) : undefined);

	// --- view mode ---
	const viewKey = $derived(isPublic ? 'share:' + src.base : placeId);
	let viewMode = $state<ViewMode>('list');
	function setMode(m: ViewMode) {
		viewMode = m;
		markFresh();
		setViewMode(viewKey, path, m);
	}

	function sortBy(k: SortKey) {
		prefs.sort = prefs.sort.key === k ? { key: k, dir: prefs.sort.dir === 'asc' ? 'desc' : 'asc' } : { key: k, dir: 'asc' };
	}

	// --- selection ---
	const selection = new SvelteSet<string>();
	let anchor = -1;
	let focused = $state(-1);
	// Multi-select mode (checkboxes + floating command bar). A plain click selects one item without it.
	let selectionMode = $state(false);
	const selected = $derived(entries.filter((e) => selection.has(e.name)));

	// Modifier gestures enter the mode once two or more items are selected; it lasts until the selection empties.
	function syncMode() {
		if (selection.size >= 2) selectionMode = true;
		else if (selection.size === 0) selectionMode = false;
	}
	function selectOnly(i: number) {
		selection.clear();
		if (entries[i]) selection.add(entries[i].name);
		anchor = i;
		focused = i;
		selectionMode = false;
	}
	function toggle(i: number) {
		const n = entries[i]?.name;
		if (n === undefined) return;
		if (selection.has(n)) selection.delete(n);
		else selection.add(n);
		anchor = i;
		focused = i;
		syncMode();
	}
	function selectRange(i: number) {
		const a = anchor < 0 ? i : anchor;
		selection.clear();
		for (let k = Math.min(a, i); k <= Math.max(a, i); k++) selection.add(entries[k].name);
		focused = i;
		syncMode();
	}
	function clearSelection() {
		selection.clear();
		selectionMode = false;
	}

	function open(e: Entry) {
		if (e.type === 'dir') {
			if (!e.readable) return toasts.error('Permission denied');
			void goto(hrefFor(joinPath(path, e.name)));
		} else if (canOpen(e)) {
			openPreview(e.name);
		}
	}

	let previewPushed = false;
	function openPreview(name: string) {
		previewPushed = true;
		void goto(hrefFor(path, { preview: name }), { noScroll: true, keepFocus: true });
	}
	function stepPreview(name: string) {
		void goto(hrefFor(path, { preview: name }), { replaceState: true, noScroll: true, keepFocus: true });
	}
	function closePreview() {
		if (previewPushed) {
			previewPushed = false;
			history.back();
		} else {
			void goto(hrefFor(path), { replaceState: true, noScroll: true, keepFocus: true });
		}
	}

	// --- touch long-press ---
	let pressTimer: ReturnType<typeof setTimeout> | null = null;
	let lastPointer: string = 'mouse';
	let longPressed = false;
	let pressEl: HTMLElement | null = null; // [M23]

	// --- hover prefetch ---
	let hoverTimer: ReturnType<typeof setTimeout> | null = null;

	// --- drag & drop ---
	let dragging: DragPayload | null = null;

	function validFiletTarget(targetDir: string): boolean {
		if (!dragging || readonly || dragging.place !== placeId) return !!dragging && !readonly;
		return dragging.paths.every((p) => !isWithin(targetDir, p) && parentPath(p) !== targetDir);
	}

	function mark(e: DragEvent, on: boolean) {
		const el = e.currentTarget as HTMLElement;
		if (on) el.dataset.drop = 'true';
		else delete el.dataset.drop;
	}

	function dropHandlers(targetDir: string): Record<string, (e: DragEvent) => void> {
		if (isPublic) return {};
		return {
			ondragover: (e: DragEvent) => {
				const types = e.dataTransfer?.types ?? [];
				if (types.includes(FILET_DRAG)) {
					if (!validFiletTarget(targetDir)) return;
					e.preventDefault();
					e.stopPropagation();
					e.dataTransfer!.dropEffect = e.altKey || e.ctrlKey ? 'copy' : 'move';
					mark(e, true);
				} else if (types.includes('Files')) {
					e.stopPropagation();
					if (readonly) return;
					e.preventDefault();
					e.dataTransfer!.dropEffect = 'copy';
					mark(e, true);
					dropOverlay = false;
				}
			},
			ondragleave: (e: DragEvent) => mark(e, false),
			ondrop: (e: DragEvent) => {
				mark(e, false);
				e.preventDefault();
				e.stopPropagation();
				dropOverlay = false;
				dragDepth = 0;
				handleDrop(e, targetDir);
			}
		};
	}

	function handleDrop(e: DragEvent, targetDir: string) {
		const dt = e.dataTransfer;
		if (!dt) return;
		const raw = dt.getData(FILET_DRAG);
		if (raw) {
			const payload = JSON.parse(raw) as DragPayload;
			if (!validFiletTarget(targetDir)) return;
			const copy = e.altKey || e.ctrlKey;
			void jobs.submit({ type: copy ? 'copy' : 'move', src: { place: payload.place, paths: payload.paths }, dst: { place: placeId, dir: targetDir } });
			clearSelection();
			return;
		}
		if (readonly || !place) return;
		const ents = entriesFromDrop(dt);
		const plain = ents ? null : fromDataTransferFiles(dt);
		void (async () => {
			const picked = ents ? await readEntries(ents) : plain!;
			startUpload(picked, targetDir);
		})();
	}

	function startUpload(picked: Picked, targetDir: string) {
		if (!place || !picked.files.length && !picked.dirs.length) return;
		const { uploads: list, dirs } = planUpload(place.id, targetDir, picked);
		void uploads.add(list, { dirs });
	}

	// whole-view OS drop zone
	let dropOverlay = $state(false);
	let dragDepth = 0;
	function viewDragEnter(e: DragEvent) {
		if (isPublic || !e.dataTransfer?.types.includes('Files')) return;
		dragDepth++;
		dropOverlay = true;
	}
	function viewDragLeave() {
		if (--dragDepth <= 0) {
			dragDepth = 0;
			dropOverlay = false;
		}
	}
	function viewDragOver(e: DragEvent) {
		if (isPublic || !e.dataTransfer?.types.includes('Files')) return;
		e.preventDefault();
		e.dataTransfer.dropEffect = readonly ? 'none' : 'copy';
		dropOverlay = true;
	}
	function viewDrop(e: DragEvent) {
		if (isPublic) return;
		e.preventDefault();
		dragDepth = 0;
		dropOverlay = false;
		if (readonly) return;
		if (e.dataTransfer?.types.includes('Files')) handleDrop(e, path);
	}

	// --- context menu & actions ---
	let menuOpen = $state(false);
	let menuX = $state(0);
	let menuY = $state(0);
	let menuEntries = $state<Entry[]>([]);

	let renameOpen = $state(false);
	let renameTarget = $state<Entry | null>(null);
	let newFolderOpen = $state(false);
	let deleteOpen = $state(false);
	let deleteTargets = $state<Entry[]>([]);
	let destOpen = $state(false);
	let destMode = $state<'copy' | 'move'>('copy');
	let destTargets = $state<Entry[]>([]);
	// Keep the picker mounted after first use so its close animation can run.
	let destLoaded = $state(false);
	$effect(() => {
		if (destOpen) destLoaded = true;
	});
	let shareOpen = $state(false);
	let streamOpen = $state(false);
	let actionTarget = $state<Entry | null>(null);
	let fabOpen = $state(false);

	function act(a: MenuAction, targets: Entry[] = menuEntries) {
		const one = targets[0];
		switch (a) {
			case 'open':
				if (one) open(one);
				break;
			case 'download':
				downloadSelection(src, path, targets);
				break;
			case 'rename':
				renameTarget = one;
				renameOpen = true;
				break;
			case 'copy':
			case 'move':
				destMode = a;
				destTargets = targets;
				destOpen = true;
				break;
			case 'share':
				actionTarget = one;
				shareOpen = true;
				break;
			case 'stream':
				actionTarget = one;
				streamOpen = true;
				break;
			case 'delete':
				deleteTargets = targets;
				deleteOpen = true;
				break;
		}
	}

	async function doRename(name: string): Promise<string | null> {
		if (!renameTarget || !place) return null;
		try {
			await post(`/api/places/${encodeURIComponent(place.id)}/rename`, { path: joinPath(path, renameTarget.name), newName: name });
			selection.clear();
			selection.add(name);
			void load();
			return null;
		} catch (e) {
			if (e instanceof ApiError && e.code === 'already_exists') return 'A file or folder with that name already exists';
			if (e instanceof ApiError && e.code === 'upload_in_progress') return 'An upload is in progress inside this folder';
			return errorMessage(e);
		}
	}

	async function doMkdir(name: string): Promise<string | null> {
		if (!place) return null;
		try {
			await post(`/api/places/${encodeURIComponent(place.id)}/mkdir`, { path: joinPath(path, name) });
			void load();
			return null;
		} catch (e) {
			if (e instanceof ApiError && e.code === 'already_exists') return 'A file or folder with that name already exists';
			return errorMessage(e);
		}
	}

	function doDelete() {
		if (!place) return;
		void jobs.submit({ type: 'trash', place: place.id, paths: deleteTargets.map((e) => joinPath(path, e.name)) });
		clearSelection();
	}

	function doCopyMove(dstPlace: string, dstDir: string) {
		if (!place) return;
		void jobs.submit({
			type: destMode,
			src: { place: place.id, paths: destTargets.map((e) => joinPath(path, e.name)) },
			dst: { place: dstPlace, dir: dstDir }
		});
		clearSelection();
	}

	// --- item controller ---
	const ctl: ItemCtl = {
		get src() {
			return src;
		},
		get dir() {
			return path;
		},
		get isPublic() {
			return isPublic;
		},
		get focused() {
			return focused;
		},
		get multi() {
			return selectionMode && selection.size > 0;
		},
		selected: (n) => selection.has(n),
		click(i, e) {
			if (longPressed) {
				longPressed = false;
				return;
			}
			if (lastPointer === 'touch') {
				if (selectionMode) toggle(i);
				else open(entries[i]);
				return;
			}
			if (e.metaKey || e.ctrlKey) toggle(i);
			else if (e.shiftKey) selectRange(i);
			else selectOnly(i);
		},
		dblclick(i) {
			if (lastPointer !== 'touch') open(entries[i]);
		},
		menu(i, e) {
			e.preventDefault();
			const ent = entries[i];
			if (!selection.has(ent.name)) selectOnly(i);
			menuEntries = selection.has(ent.name) && selection.size > 1 ? selected : [ent];
			const rect = (e.currentTarget as HTMLElement | null)?.getBoundingClientRect();
			menuX = e.clientX || rect?.left || 0;
			menuY = e.clientY || rect?.bottom || 0;
			menuOpen = true;
		},
		toggle,
		pointerdown(i, e) {
			lastPointer = e.pointerType;
			if (e.pointerType !== 'touch') return;
			pressEl = e.currentTarget as HTMLElement;
			pressEl.dataset.pressing = '';
			pressTimer = setTimeout(() => {
				const el = pressEl;
				if (el) {
					delete el.dataset.pressing;
					el.dataset.pressedPop = '';
					setTimeout(() => delete el.dataset.pressedPop, 320);
				}
				longPressed = true;
				selectionMode = true;
				if (!selection.has(entries[i].name)) toggle(i);
				navigator.vibrate?.(10);
			}, 500);
		},
		pointerup() {
			if (pressTimer) clearTimeout(pressTimer);
			pressTimer = null;
			if (pressEl) delete pressEl.dataset.pressing;
			pressEl = null;
		},
		dragstart(i, e) {
			if (isPublic || !place) return;
			const ent = entries[i];
			if (!selection.has(ent.name)) selectOnly(i);
			dragging = { place: place.id, paths: selected.map((s) => joinPath(path, s.name)) };
			e.dataTransfer!.setData(FILET_DRAG, JSON.stringify(dragging));
			e.dataTransfer!.setData('text/plain', selected.map((s) => s.name).join('\n'));
			e.dataTransfer!.effectAllowed = readonly ? 'copy' : 'copyMove';
			const done = () => {
				dragging = null;
				window.removeEventListener('dragend', done, true);
			};
			window.addEventListener('dragend', done, true);
		},
		drop: (e) => (e.type === 'dir' ? dropHandlers(joinPath(path, e.name)) : {}),
		hover(i, on) {
			if (hoverTimer) clearTimeout(hoverTimer);
			hoverTimer = null;
			const e = entries[i];
			if (on && e?.type === 'dir' && e.readable && !viewport.coarse) {
				hoverTimer = setTimeout(() => listingCache.prefetch(src, joinPath(path, e.name)), 150);
			}
		}
	};

	// --- keyboard ---
	let listView: FileList | undefined = $state();
	let gridView: FileGrid | undefined = $state();
	let gridLayout = $state({ folderCols: 1, fileCols: 1, dirCount: 0 });
	let searchInput: HTMLInputElement | undefined = $state();

	function moveFocus(delta: number) {
		const n = entries.length;
		if (!n) return;
		const next = Math.max(0, Math.min(n - 1, (focused < 0 ? -1 : focused) + delta));
		focused = next;
		if (viewMode === 'list') listView?.scrollToIndex(next);
		else gridView?.scrollToIndex(next);
	}

	function gridDelta(dir: 'up' | 'down'): number {
		const { folderCols, fileCols, dirCount } = gridLayout;
		const i = Math.max(0, focused);
		const inFolders = i < dirCount;
		const cols = inFolders ? folderCols : fileCols;
		if (dir === 'down') {
			if (inFolders && i + cols >= dirCount) return dirCount + Math.min((i % cols), fileCols - 1) - i;
			return cols;
		}
		if (!inFolders && i - cols < dirCount) {
			if (!dirCount) return -cols;
			const lastRowStart = dirCount - ((dirCount - 1) % folderCols) - 1;
			return Math.min(lastRowStart + ((i - dirCount) % fileCols), dirCount - 1) - i;
		}
		return -cols;
	}

	function onKey(e: KeyboardEvent) {
		if (previewEntry || menuOpen || renameOpen || newFolderOpen || deleteOpen || destOpen || shareOpen || streamOpen) return;
		const t = e.target as HTMLElement;
		if (t.closest('input, textarea, select, [contenteditable], [role=dialog], [role=menu]')) return;
		const mod = e.ctrlKey || e.metaKey;
		switch (e.key) {
			case 'ArrowDown':
				e.preventDefault();
				moveFocus(viewMode === 'grid' ? gridDelta('down') : 1);
				if (e.shiftKey) selectRange(focused);
				break;
			case 'ArrowUp':
				if (e.altKey) {
					e.preventDefault();
					if (path !== '/') void goto(hrefFor(parentPath(path)));
					break;
				}
				e.preventDefault();
				moveFocus(viewMode === 'grid' ? gridDelta('up') : -1);
				if (e.shiftKey) selectRange(focused);
				break;
			case 'ArrowRight':
				if (viewMode === 'grid') {
					e.preventDefault();
					moveFocus(1);
				}
				break;
			case 'ArrowLeft':
				if (viewMode === 'grid') {
					e.preventDefault();
					moveFocus(-1);
				}
				break;
			case 'Enter':
				if (focused >= 0 && entries[focused]) open(entries[focused]);
				break;
			case ' ':
				if (focused >= 0) {
					e.preventDefault();
					toggle(focused);
				}
				break;
			case 'Backspace':
				if (path !== '/') void goto(hrefFor(parentPath(path)));
				break;
			case 'Delete':
				if (!readonly && selected.length) act('delete', selected);
				break;
			case 'F2':
				if (!readonly && focused >= 0 && entries[focused]) {
					e.preventDefault();
					act('rename', [entries[focused]]);
				}
				break;
			case 'a':
			case 'A':
				if (mod) {
					e.preventDefault();
					selection.clear();
					for (const en of entries) selection.add(en.name);
					syncMode();
				}
				break;
			case '/':
				if (!isPublic) {
					e.preventDefault();
					searchOpen = true;
					setTimeout(() => searchInput?.focus());
				}
				break;
			case 'Escape':
				if (q) clearSearch();
				else clearSelection();
				break;
		}
	}

	// --- search ---
	let searchText = $state('');
	let searchOpen = $state(false);
	let spins = $state(0); // [M26]
	let searchTimer: ReturnType<typeof setTimeout> | null = null;
	$effect(() => {
		searchText = q;
		if (q) searchOpen = true;
	});
	function onSearchInput() {
		if (searchTimer) clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			const v = searchText.trim();
			if (v.length >= 2) void goto(hrefFor(path, { q: v }), { replaceState: !!q, keepFocus: true, noScroll: true });
			else if (q) void goto(hrefFor(path), { replaceState: true, keepFocus: true, noScroll: true });
		}, 250);
	}
	function clearSearch() {
		searchText = '';
		searchOpen = false;
		void goto(hrefFor(path), { replaceState: true, keepFocus: true, noScroll: true });
	}

	// --- upload pickers ---
	let fileInput: HTMLInputElement | undefined = $state();
	let folderInput: HTMLInputElement | undefined = $state();
	function picked(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (input.files?.length) startUpload(fromFileList(input.files), path);
		input.value = '';
	}

	let scrollEl: HTMLElement | null = $state(null);
	const canWrite = $derived(!readonly && (listing?.dirWritable ?? false));
	const sortLabels: Record<SortKey, string> = { name: 'Name', size: 'Size', mtime: 'Modified', type: 'Type' };
	const dirName = $derived(path === '/' ? rootLabel : path.slice(path.lastIndexOf('/') + 1));
</script>

<svelte:window onkeydown={onKey} />

<input bind:this={fileInput} type="file" multiple class="hidden" onchange={picked} data-testid="file-input" />
<input bind:this={folderInput} type="file" webkitdirectory class="hidden" onchange={picked} data-testid="folder-input" />

<div class="flex h-dvh flex-col">
	{#if isPublic}
		{@render publicHeader?.()}
	{:else}
		<header class="flex flex-col border-b border-line">
			<div class="flex h-14 items-center gap-2 px-2 md:px-3">
				<a href="/" class="hidden items-center gap-1.5 px-1 font-semibold md:flex" aria-label="Filet home">
					<img src="/favicon.svg" alt="" class="size-6" />Filet
				</a>
				<div class="min-w-0 max-md:flex-1 md:max-w-60"><PlaceChooser current={place ?? undefined} /></div>
				{#if !viewport.mobile}
					<Breadcrumbs {rootLabel} {path} href={(d) => hrefFor(d)} dropTarget={dropHandlers} />
				{/if}
				{#if viewport.mobile && !searchOpen}
					<button class="icon-btn" aria-label="Search" onclick={() => { searchOpen = true; setTimeout(() => searchInput?.focus()); }}><Search class="size-5" /></button>
				{/if}
				{#if !viewport.mobile || searchOpen}
					<!-- [M39] m-search -->
					<div class="m-search relative max-md:absolute max-md:inset-x-2 max-md:z-20 max-md:bg-bg md:w-72">
						<Search class="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted" />
						<input
							bind:this={searchInput}
							bind:value={searchText}
							oninput={onSearchInput}
							onkeydown={(e) => e.key === 'Escape' && clearSearch()}
							class="input pr-8 pl-8"
							placeholder="Search in {place?.name ?? ''}"
							aria-label="Search in {place?.name ?? ''}"
							data-testid="search-input"
						/>
						{#if searchText || viewport.mobile}
							<button class="icon-btn absolute top-0 right-0" aria-label="Clear search" onclick={clearSearch}><X class="size-4" /></button>
						{/if}
					</div>
				{/if}
				<AppMenu placeId={place?.id} />
			</div>
			{#if viewport.mobile}
				<div class="flex h-10 items-center px-2"><Breadcrumbs {rootLabel} {path} href={(d) => hrefFor(d)} dropTarget={dropHandlers} /></div>
			{/if}
		</header>
	{/if}

	{#if !q}
		<div class="flex h-11 items-center gap-1 border-b border-line px-2 text-sm md:px-3">
			<span class="text-muted md:hidden">{entries.length} items</span>
			{#if place?.readonly}<span class="badge ml-1 bg-surface-2 text-muted"><Lock class="mr-1 size-3" />Read-only</span>{/if}
			<span class="flex-1"></span>
			{#if !viewport.mobile && canWrite}
				<DropdownMenu.Root>
					<DropdownMenu.Trigger class="btn h-8" data-testid="upload-menu"><Upload class="size-4" />Upload<ChevronDown class="m-chev size-3" /></DropdownMenu.Trigger><!-- [M25] -->
					<DropdownMenu.Portal>
						<DropdownMenu.Content class="menu" align="end" sideOffset={4}>
							<DropdownMenu.Item class="menu-item" onSelect={() => fileInput?.click()}><Upload class="size-4" />Upload files</DropdownMenu.Item>
							<DropdownMenu.Item class="menu-item" onSelect={() => folderInput?.click()}><FolderUp class="size-4" />Upload folder</DropdownMenu.Item>
						</DropdownMenu.Content>
					</DropdownMenu.Portal>
				</DropdownMenu.Root>
				<button class="icon-btn size-8" aria-label="New folder" title="New folder" onclick={() => (newFolderOpen = true)}><FolderPlus class="size-4" /></button>
			{/if}
			<!-- [M43] m-toggle / m-toggle-icon -->
			<ToggleGroup.Root type="single" value={viewMode} onValueChange={(v) => v && setMode(v as ViewMode)} class="flex rounded-md border border-line p-0.5" aria-label="View">
				<ToggleGroup.Item value="list" class="m-toggle flex size-7 items-center justify-center rounded hover:not-data-[state=on]:bg-surface-2/60 data-[state=on]:bg-surface-2" aria-label="List view" title="List view"><List class="m-toggle-icon size-4" /></ToggleGroup.Item>
				<ToggleGroup.Item value="grid" class="m-toggle flex size-7 items-center justify-center rounded hover:not-data-[state=on]:bg-surface-2/60 data-[state=on]:bg-surface-2" aria-label="Grid view" title="Grid view"><LayoutGrid class="m-toggle-icon size-4" /></ToggleGroup.Item>
			</ToggleGroup.Root>
			<DropdownMenu.Root>
				<DropdownMenu.Trigger class="btn btn-ghost h-8 px-2" aria-label="Sort"><ArrowUpDown class="size-4" /><span class="max-md:hidden">{sortLabels[prefs.sort.key]}</span></DropdownMenu.Trigger>
				<DropdownMenu.Portal>
					<DropdownMenu.Content class="menu" align="end" sideOffset={4}>
						{#each Object.entries(sortLabels) as [k, label] (k)}
							<DropdownMenu.Item class="menu-item" onSelect={() => sortBy(k as SortKey)}>
								<span class="flex size-4 items-center">{#if prefs.sort.key === k}<Check class="m-check size-4" />{/if}</span>{label}
								{#if prefs.sort.key === k}<span class="ml-auto text-muted">{prefs.sort.dir === 'asc' ? '↑' : '↓'}</span>{/if}
							</DropdownMenu.Item>
						{/each}
					</DropdownMenu.Content>
				</DropdownMenu.Portal>
			</DropdownMenu.Root>
			<button class="icon-btn size-8" aria-label="Refresh" title="Refresh" onclick={() => { spins++; void load(); }}>
				{#key spins}<RefreshCw class="size-4 {spins ? 'm-spin-once' : ''}" />{/key}
			</button>
		</div>
	{/if}

	<!-- Clicking empty space clears the selection; Esc does the same from the keyboard. -->
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
	<main
		class="relative min-h-0 flex-1 overflow-auto"
		bind:this={scrollEl}
		ondragenter={viewDragEnter}
		ondragleave={viewDragLeave}
		ondragover={viewDragOver}
		ondrop={viewDrop}
		data-fresh={fresh || undefined}
		onclick={(e) => {
			if (e.target === e.currentTarget || (e.target as HTMLElement).dataset.empty) clearSelection();
		}}
		aria-label="Files"
	>
		{#if !loaded && !q}
			<!-- [M50] -->
			<div class="m-loadbar sticky top-0 z-20 -mb-0.5 h-0.5" aria-hidden="true"></div>
		{/if}
		{#if q && place}
			<SearchResults place={place.id} {q} />
		{:else if loadError && !listing}
			<div class="m-fade-up flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-muted"><!-- [M20] -->
				{#if loadError.status === 403}
					<Lock class="size-10" /><p>You don't have permission to open this folder</p>
				{:else if loadError.status === 404}
					<FolderX class="size-10" /><p>This folder no longer exists</p>
					<button class="btn" onclick={() => goto(hrefFor(parentPath(path)))}>Go to parent folder</button>
				{:else}
					<p>{loadError.message}</p>
					<button class="btn" onclick={() => load()}>Retry</button>
				{/if}
			</div>
		{:else if loaded && listing && entries.length === 0 && placeholders.length === 0}
			<!-- [M20] -->
			<div class="m-fade-up flex h-full flex-col items-center justify-center gap-1 p-6 text-center text-muted" data-empty="true">
				<FolderOpen class="m-float mb-2 size-10" strokeWidth={1.25} />
				<p>This folder is empty</p>
				{#if canWrite}<p class="text-sm">Drop files here or use Upload</p>{/if}
			</div>
		{:else if listing}
			{#if viewMode === 'list'}
				<FileList bind:this={listView} {entries} {placeholders} {ctl} {scrollEl} sort={prefs.sort} onSort={sortBy} />
			{:else}
				<FileGrid bind:this={gridView} bind:layout={gridLayout} {entries} {placeholders} {ctl} {scrollEl} />
			{/if}
			<div class="h-24" data-empty="true"></div>
		{/if}
		{#if dropOverlay}
			<div class="pointer-events-none sticky inset-0 -mt-[100%] flex h-full items-center justify-center p-4">
				<!-- [M21] -->
				<div class="m-drop flex h-full w-full flex-col items-center justify-center gap-3 rounded-xl border-2 border-dashed {readonly ? 'border-danger text-danger' : 'border-accent text-accent'} bg-bg/80 text-lg font-medium">
					{#if readonly}<Lock class="size-8" />{:else}<Upload class="m-bounce size-8" />{/if}
					{readonly ? 'This place is read-only' : `Drop to upload to ${dirName}`}
				</div>
			</div>
		{/if}
	</main>
</div>

{#if selectionMode && selection.size > 0}
	<SelectionBar entries={selected} {readonly} {isPublic} onAction={(a) => act(a, selected)} onClear={clearSelection} />
{:else if viewport.mobile && canWrite && !q}
	<!-- [M24] m-fab, [M04] data-ripple -->
	<button
		class="m-fab fixed right-4 bottom-20 z-30 flex size-14 items-center justify-center rounded-full bg-accent text-accent-fg shadow-lg"
		aria-label="Add"
		data-open={fabOpen}
		data-ripple
		onclick={() => (fabOpen = true)}
		data-testid="fab"><Plus class="size-6" /></button
	>
	<Dialog.Root bind:open={fabOpen}>
		<Dialog.Portal>
			<Dialog.Overlay class="dialog-overlay" />
			<Dialog.Content class="m-sheet fixed inset-x-0 bottom-0 z-50 flex flex-col rounded-t-2xl border border-line bg-bg p-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] outline-none">
				<Dialog.Title class="sr-only">Add</Dialog.Title>
				<div class="mx-auto mb-2 h-1 w-10 rounded-full bg-line"></div>
				<button class="menu-item h-12 hover:bg-surface-2" onclick={() => { fabOpen = false; fileInput?.click(); }}><Upload class="size-5" />Upload files</button>
				<button class="menu-item h-12 hover:bg-surface-2" onclick={() => { fabOpen = false; folderInput?.click(); }}><FolderUp class="size-5" />Upload folder</button>
				<button class="menu-item h-12 hover:bg-surface-2" onclick={() => { fabOpen = false; newFolderOpen = true; }}><FolderPlus class="size-5" />New folder</button>
			</Dialog.Content>
		</Dialog.Portal>
	</Dialog.Root>
{/if}

<ItemMenu bind:open={menuOpen} x={menuX} y={menuY} entries={menuEntries} {readonly} {isPublic} onAction={(a) => act(a)} />

{#if !isPublic && place}
	<NameDialog bind:open={renameOpen} title="Rename" initial={renameTarget?.name ?? ''} confirmLabel="Rename" selectStem={renameTarget?.type !== 'dir'} onSubmit={doRename} />
	<NameDialog bind:open={newFolderOpen} title="New folder" initial="" confirmLabel="Create" onSubmit={doMkdir} />
	<ConfirmDialog
		bind:open={deleteOpen}
		title={deleteTargets.length === 1 ? `Move “${deleteTargets[0]?.name}” to Trash?` : `Move ${deleteTargets.length} items to Trash?`}
		description="You can restore them from Trash"
		confirmLabel="Move to Trash"
		danger
		onConfirm={doDelete}
	/>
	{#if destOpen || destLoaded}
		{#await import('../DestinationPicker.svelte') then { default: DestinationPicker }}
			<DestinationPicker
				bind:open={destOpen}
				title={destMode === 'copy' ? 'Copy to…' : 'Move to…'}
				confirmLabel={destMode === 'copy' ? 'Copy here' : 'Move here'}
				initialPlace={place.id}
				initialDir={path}
				onPick={doCopyMove}
			/>
		{/await}
	{/if}
	{#if actionTarget && (shareOpen || streamOpen)}
		{#await import('../ShareDialog.svelte') then { default: ShareDialog }}
			<ShareDialog bind:open={shareOpen} place={place.id} path={joinPath(path, actionTarget.name)} name={actionTarget.name} />
		{/await}
		{#await import('../StreamDialog.svelte') then { default: StreamDialog }}
			<StreamDialog bind:open={streamOpen} place={place.id} path={joinPath(path, actionTarget.name)} />
		{/await}
	{/if}
{/if}

{#if previewEntry}
	{#await import('../preview/PreviewOverlay.svelte') then { default: PreviewOverlay }}
	<PreviewOverlay
		{src}
		{place}
		dir={path}
		entry={previewEntry}
		sequence={previewSeq}
		{hrefFor}
		onStep={stepPreview}
		onClose={closePreview}
		onShare={(e) => {
			actionTarget = e;
			shareOpen = true;
		}}
		onStream={(e) => {
			actionTarget = e;
			streamOpen = true;
		}}
	/>
	{/await}
{/if}
