/// <reference lib="webworker" />
// Built by @vite-pwa/sveltekit (injectManifest): precache, SPA navigation
// fallback, a CacheFirst thumbnail cache and the Web Share Target handler.
import { set } from 'idb-keyval';
import { CacheableResponsePlugin } from 'workbox-cacheable-response';
import { ExpirationPlugin } from 'workbox-expiration';
import { cleanupOutdatedCaches, createHandlerBoundToURL, precacheAndRoute } from 'workbox-precaching';
import { NavigationRoute, registerRoute } from 'workbox-routing';
import { CacheFirst } from 'workbox-strategies';

declare const self: ServiceWorkerGlobalScope & { __WB_MANIFEST: (string | { url: string; revision: string | null })[] };

// Share-target POSTs must be handled before Workbox's router sees the request.
self.addEventListener('fetch', (event) => {
	const url = new URL(event.request.url);
	if (event.request.method === 'POST' && url.pathname === '/share-target') {
		event.respondWith(receiveShare(event.request));
	}
});

async function receiveShare(req: Request): Promise<Response> {
	const form = await req.formData();
	const files = form.getAll('files').filter((f): f is File => f instanceof File && f.name !== '');
	const id = crypto.randomUUID();
	await set(`share:${id}`, {
		files,
		title: form.get('title')?.toString() ?? '',
		text: (form.get('text') ?? form.get('url'))?.toString() ?? '',
		createdAt: Date.now()
	});
	return Response.redirect(`/share?id=${id}`, 303);
}

precacheAndRoute(self.__WB_MANIFEST);
cleanupOutdatedCaches();

registerRoute(
	new NavigationRoute(createHandlerBoundToURL('/index.html'), {
		denylist: [/^\/api\//, /^\/stream\//, /^\/share-target/]
	})
);

registerRoute(
	({ url, request }) => request.method === 'GET' && /^\/api\/places\/[^/]+\/thumb\//.test(url.pathname),
	new CacheFirst({
		cacheName: 'filet-thumbs',
		plugins: [
			new ExpirationPlugin({ maxEntries: 20000, maxAgeSeconds: 60 * 24 * 3600 }),
			new CacheableResponsePlugin({ statuses: [200] })
		]
	})
);

self.addEventListener('message', (event) => {
	if (event.data?.type === 'SKIP_WAITING') void self.skipWaiting();
});
