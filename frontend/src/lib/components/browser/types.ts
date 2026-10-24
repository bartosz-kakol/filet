import type { Source } from '$lib/api/source';
import type { Entry } from '$lib/api/types';

export interface Placeholder {
	key: string;
	name: string;
	size: number;
	progress: number;
	leftover: boolean;
}

/** Callbacks and state the list and grid views share. */
export interface ItemCtl {
	src: Source;
	dir: string;
	isPublic: boolean;
	focused: number;
	/** Multi-select mode: checkboxes are shown. */
	multi: boolean;
	selected(name: string): boolean;
	click(i: number, e: MouseEvent): void;
	dblclick(i: number): void;
	menu(i: number, e: MouseEvent): void;
	toggle(i: number): void;
	pointerdown(i: number, e: PointerEvent): void;
	pointerup(): void;
	dragstart(i: number, e: DragEvent): void;
	drop(entry: Entry): Record<string, (e: DragEvent) => void>;
	hover(i: number, on: boolean): void;
}
