<script lang="ts">
	import {
		File,
		FileArchive,
		FileCode,
		FileImage,
		FileMusic,
		FileSpreadsheet,
		FileText,
		FileType,
		FileVideoCamera,
		Folder
	} from '@lucide/svelte';
	import type { Kind } from '$lib/api/types';

	let { kind, class: cls = 'size-5', strokeWidth = 1.75 }: { kind: Kind | string; class?: string; strokeWidth?: number } =
		$props();

	const icons = {
		dir: Folder,
		image: FileImage,
		'image-render': FileImage,
		video: FileVideoCamera,
		audio: FileMusic,
		markdown: FileText,
		text: FileCode,
		csv: FileSpreadsheet,
		pdf: FileText,
		archive: FileArchive,
		font: FileType
	} as const;
	const Icon = $derived(icons[kind as keyof typeof icons] ?? File);
	const color = $derived(kind === 'dir' ? 'text-accent' : 'text-muted');
</script>

<Icon class="{cls} {color} shrink-0" {strokeWidth} aria-hidden="true" />
