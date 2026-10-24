import { kindOf } from '$lib/files/kinds';
import { prefs } from '$lib/prefs/prefs.svelte';
import { conflicts } from '$lib/stores/conflicts.svelte';
import { places } from '$lib/stores/places.svelte';
import { session } from '$lib/stores/session.svelte';
import { HasherPool } from './hasher';
import { UploadManager, type UploadItem } from './manager.svelte';
import { httpTransport } from './transport';

let refreshTimer: ReturnType<typeof setTimeout> | null = null;
function refreshPlacesSoon() {
	if (refreshTimer) return;
	refreshTimer = setTimeout(() => {
		refreshTimer = null;
		void places.refresh();
	}, 1000);
}

export const uploads = new UploadManager({
	transport: httpTransport,
	hasher: new HasherPool(),
	concurrency: () => prefs.uploadConcurrency,
	placeName: (id) => places.name(id),
	onAuthRequired: () => {
		session.reloginOpen = true;
	},
	onCompleted: () => refreshPlacesSoon(),
	async prompt(c) {
		const it: UploadItem = c.item;
		const incomingPreview = kindOf(it.name) === 'image' ? URL.createObjectURL(it.file) : undefined;
		const choice = await conflicts.ask({
			name: it.name,
			dir: it.dir,
			existing: c.existing,
			incoming: { size: it.size, mtime: it.mtime, previewUrl: incomingPreview },
			suggestedName: c.suggestedName,
			remaining: c.remaining,
			uploading: c.status === 'uploading'
		});
		if (incomingPreview) URL.revokeObjectURL(incomingPreview);
		return choice ?? { action: 'skip', applyToAll: false };
	}
});

session.onRelogin(() => uploads.resumeAuth());

if (typeof window !== 'undefined') {
	window.addEventListener('online', () => uploads.online());
	window.addEventListener('beforeunload', (e) => {
		if (uploads.items.some((i) => i.active || (i.state === 'queued' && i.ready))) {
			e.preventDefault();
			e.returnValue = '';
		}
	});
}

