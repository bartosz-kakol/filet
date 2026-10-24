export interface Toast {
	id: number;
	text: string;
	kind: 'info' | 'error' | 'success';
	action?: { label: string; run: () => void };
	sticky?: boolean;
	/** Auto-dismiss delay; unset for sticky toasts. */
	ms?: number;
}

let seq = 0;

class Toasts {
	list = $state<Toast[]>([]);

	show(text: string, kind: Toast['kind'] = 'info', opts: { action?: Toast['action']; sticky?: boolean; ms?: number } = {}) {
		const t: Toast = { id: ++seq, text, kind, action: opts.action, sticky: opts.sticky, ms: opts.sticky ? undefined : (opts.ms ?? 4500) };
		this.list.push(t);
		if (t.ms !== undefined) setTimeout(() => this.dismiss(t.id), t.ms);
		return t.id;
	}
	error(text: string) {
		return this.show(text, 'error');
	}
	dismiss(id: number) {
		this.list = this.list.filter((t) => t.id !== id);
	}
}

export const toasts = new Toasts();
