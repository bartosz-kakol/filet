import { del, errorMessage, get, post } from '$lib/api/client';
import { events } from '$lib/api/events';
import type { Job, JobSpec } from '$lib/api/types';
import { baseName, parentPath } from '$lib/files/path';
import { conflicts } from '$lib/stores/conflicts.svelte';
import { places } from '$lib/stores/places.svelte';
import { toasts } from '$lib/stores/toast.svelte';

export const TERMINAL_JOB = new Set(['completed', 'failed', 'cancelled', 'interrupted']);

class Jobs {
	list = $state<Job[]>([]);
	#prompted = new Set<string>();
	#started = false;

	start() {
		if (this.#started) return;
		this.#started = true;
		events.on('job.updated', (j: Job) => this.upsert(j));
		events.on('job.removed', (d: { id: string }) => this.remove(d.id));
		events.onOpen(() => void this.refresh());
		void this.refresh();
	}

	async refresh() {
		try {
			const l = await get<Job[]>('/api/jobs');
			this.list = l;
			for (const j of l) this.#maybePrompt(j);
		} catch {
			/* offline */
		}
	}

	get active() {
		return this.list.filter((j) => !TERMINAL_JOB.has(j.status));
	}

	upsert(j: Job) {
		const i = this.list.findIndex((x) => x.id === j.id);
		const prev = i >= 0 ? this.list[i] : null;
		// HTTP responses can arrive after newer SSE updates; a terminal job
		// never becomes active again, so an older snapshot is ignored.
		if (prev && TERMINAL_JOB.has(prev.status) && !TERMINAL_JOB.has(j.status)) return;
		if (i >= 0) this.list[i] = j;
		else this.list.unshift(j);
		if (TERMINAL_JOB.has(j.status) && prev && !TERMINAL_JOB.has(prev.status)) void places.refresh();
		this.#maybePrompt(j);
	}

	remove(id: string) {
		this.list = this.list.filter((j) => j.id !== id);
	}

	async submit(spec: JobSpec): Promise<Job | null> {
		try {
			const j = await post<Job>('/api/jobs', spec);
			this.upsert(j);
			return j;
		} catch (e) {
			toasts.error(errorMessage(e));
			return null;
		}
	}

	async cancel(id: string) {
		try {
			this.upsert(await post<Job>(`/api/jobs/${id}/cancel`));
		} catch (e) {
			toasts.error(errorMessage(e));
		}
	}

	async dismiss(id: string) {
		try {
			await del(`/api/jobs/${id}`);
		} catch {
			/* already gone */
		}
		this.remove(id);
	}

	clearFinished() {
		for (const j of this.list) if (TERMINAL_JOB.has(j.status)) void this.dismiss(j.id);
	}

	#maybePrompt(j: Job) {
		if (j.status !== 'awaiting_input' || !j.pendingConflict) return;
		if (this.#prompted.has(j.pendingConflict.id)) return;
		this.#prompted.add(j.pendingConflict.id);
		void this.prompt(j);
	}

	/** Opens the conflict dialog for a job's pending conflict. */
	async prompt(j: Job) {
		const c = j.pendingConflict;
		if (!c) return;
		this.#prompted.add(c.id);
		const choice = await conflicts.ask({
			name: baseName(c.dst.path),
			dir: parentPath(c.dst.path),
			existing: c.dst.entry,
			incoming: { size: c.src.entry.size, mtime: c.src.entry.mtime, isDir: c.src.entry.type === 'dir' },
			suggestedName: c.suggestedName,
			remaining: 0,
			noOverwrite: c.sameItem || c.uploadInProgress
		});
		if (!choice) return; // dismissed; "Needs your decision" reopens it
		try {
			this.upsert(
				await post<Job>(`/api/jobs/${j.id}/resolve`, {
					conflictId: c.id,
					action: choice.action,
					applyToAll: choice.applyToAll
				})
			);
		} catch (e) {
			toasts.error(errorMessage(e));
		}
	}
}

export const jobs = new Jobs();
