import type { Component } from 'svelte';

export interface ViewerAction {
	id: string;
	label: string;
	icon: Component;
	onclick: () => void;
	active?: boolean;
	disabled?: boolean;
	title?: string;
}

/** Shared between the preview overlay and the active viewer. */
export class ViewerControls {
	actions = $state<ViewerAction[]>([]);
	/** False while an image is zoomed (disables swipe navigation). */
	canSwipe = $state(true);
	/** Set by the viewer once the main content is displayed. */
	loaded = $state(false);
	/** Natural pixel size of an image, for the info panel. */
	dims = $state<{ w: number; h: number } | null>(null);
	/** Viewer-specific key handling; return true when handled. */
	onKey: ((e: KeyboardEvent) => boolean) | null = null;

	reset() {
		this.actions = [];
		this.canSwipe = true;
		this.loaded = false;
		this.dims = null;
		this.onKey = null;
	}
}
