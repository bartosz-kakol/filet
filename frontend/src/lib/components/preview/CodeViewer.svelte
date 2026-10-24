<script lang="ts">
	import { Braces, ClipboardCopy, TextWrap } from '@lucide/svelte';
	import { Compartment, EditorState } from '@codemirror/state';
	import { EditorView, highlightSpecialChars, keymap, lineNumbers } from '@codemirror/view';
	import { LanguageDescription, defaultHighlightStyle, foldGutter, syntaxHighlighting } from '@codemirror/language';
	import { languages } from '@codemirror/language-data';
	import { search, searchKeymap } from '@codemirror/search';
	import { oneDark } from '@codemirror/theme-one-dark';
	import { onDestroy, untrack } from 'svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import { ext } from '$lib/files/kinds';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import type { ViewerControls } from '$lib/preview/controls.svelte';
	import { fetchText } from '$lib/preview/prefetch';
	import { appTheme } from '$lib/preview/theme.svelte';
	import { toasts } from '$lib/stores/toast.svelte';

	let {
		src,
		path,
		entry,
		controls,
		text: given,
		filename,
		manageControls = true
	}: {
		src?: Source;
		path?: string;
		entry: Entry;
		controls: ViewerControls;
		/** Use this text instead of fetching the file. */
		text?: string;
		/** Name used to pick the language (defaults to the entry name). */
		filename?: string;
		manageControls?: boolean;
	} = $props();

	let host: HTMLDivElement | undefined = $state();
	let view: EditorView | null = null;
	let text = $state<string | null>(null);
	let error = $state<string | null>(null);
	let pretty = $state(false);
	const wrap = new Compartment();
	const theme = new Compartment();
	const lang = new Compartment();

	const isJson = $derived(['json', 'jsonc'].includes(ext(entry.name)));
	const prettyText = $derived.by(() => {
		if (!isJson || text === null) return null;
		try {
			return JSON.stringify(JSON.parse(text), null, 2);
		} catch {
			return null;
		}
	});

	$effect(() => {
		if (given !== undefined) {
			text = given;
			return;
		}
		if (!src || !path) return;
		fetchText(src.raw(path))
			.then((t) => (text = t))
			.catch((e) => (error = String(e?.message ?? e)));
	});

	const themeExt = () => (appTheme.dark ? oneDark : EditorView.theme({}, { dark: false }));

	$effect(() => {
		if (!host || text === null) return;
		const doc = pretty && prettyText !== null ? prettyText : text;
		if (view) {
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: doc } });
			return;
		}
		view = new EditorView({
			parent: host,
			state: EditorState.create({
				doc,
				extensions: [
					EditorState.readOnly.of(true),
					EditorView.editable.of(false),
					lineNumbers(),
					highlightSpecialChars(),
					foldGutter(),
					syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
					search({ top: true }),
					keymap.of(searchKeymap),
					wrap.of(untrack(() => prefs.softWrap) ? EditorView.lineWrapping : []),
					theme.of(untrack(themeExt)),
					lang.of([]),
					EditorView.theme({ '&': { height: '100%', fontSize: '13px' }, '.cm-scroller': { fontFamily: 'var(--font-mono)' } })
				]
			})
		});
		controls.loaded = true;
		const desc = LanguageDescription.matchFilename(languages, filename ?? entry.name);
		desc?.load().then((support) => view?.dispatch({ effects: lang.reconfigure(support) }));
	});

	$effect(() => {
		view?.dispatch({ effects: wrap.reconfigure(prefs.softWrap ? EditorView.lineWrapping : []) });
	});
	$effect(() => {
		void appTheme.dark;
		view?.dispatch({ effects: theme.reconfigure(themeExt()) });
	});

	$effect(() => {
		if (!manageControls) return;
		const acts = [
			{ id: 'wrap', label: 'Soft wrap', icon: TextWrap, active: prefs.softWrap, onclick: () => (prefs.softWrap = !prefs.softWrap) },
			{
				id: 'copy',
				label: 'Copy all',
				icon: ClipboardCopy,
				onclick: () => {
					void navigator.clipboard.writeText(view?.state.doc.toString() ?? '').then(() => toasts.show('Copied'));
				}
			}
		];
		if (isJson)
			acts.unshift({
				id: 'pretty',
				label: 'Pretty-print',
				icon: Braces,
				active: pretty,
				disabled: prettyText === null,
				title: prettyText === null ? 'Invalid JSON' : 'Pretty-print',
				onclick: () => (pretty = !pretty)
			} as (typeof acts)[number]);
		controls.actions = acts;
	});

	onDestroy(() => view?.destroy());
</script>

{#if error}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Could not load: {error}</div>
{:else}
	<div class="h-full {appTheme.dark ? 'bg-[#282c34]' : 'bg-white text-zinc-900'}" bind:this={host} data-testid="code-viewer"></div>
{/if}
