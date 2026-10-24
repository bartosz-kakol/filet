import type { Entry, JobConflict } from '$lib/api/types';

/** What the conflict dialog shows, for uploads and jobs alike. */
export interface ConflictView {
	name: string;
	dir: string;
	existing?: Partial<Entry> & { previewUrl?: string };
	incoming?: { size: number; mtime: number; previewUrl?: string; isDir?: boolean };
	suggestedName: string;
	remaining: number;
	uploading?: boolean;
	noOverwrite?: boolean;
}

export interface ConflictChoice {
	action: 'overwrite' | 'rename' | 'skip';
	applyToAll: boolean;
}

interface Pending {
	view: ConflictView;
	resolve: (c: ConflictChoice | null) => void;
	jobConflict?: JobConflict;
}

/** A FIFO of conflict prompts rendered one at a time by ConflictDialog. */
class ConflictQueue {
	queue = $state<Pending[]>([]);

	get current(): Pending | undefined {
		return this.queue[0];
	}

	ask(view: ConflictView): Promise<ConflictChoice | null> {
		return new Promise((resolve) => this.queue.push({ view, resolve }));
	}

	answer(choice: ConflictChoice | null) {
		const p = this.queue.shift();
		p?.resolve(choice);
	}
}

export const conflicts = new ConflictQueue();
