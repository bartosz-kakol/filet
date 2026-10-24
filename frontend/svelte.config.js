import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

// The theme bootstrap in app.html runs before first paint; allow it by hash.
const appHtml = readFileSync(new URL('./src/app.html', import.meta.url), 'utf8');
const bootstrap = appHtml.match(/<script>([\s\S]*?)<\/script>/)?.[1] ?? '';
const bootstrapHash = `sha256-${createHash('sha256').update(bootstrap).digest('base64')}`;

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	kit: {
		adapter: adapter({ fallback: 'index.html', pages: 'build', assets: 'build', strict: false }),
		serviceWorker: { register: false },
		csp: {
			mode: 'hash',
			directives: {
				'default-src': ['self'],
				'script-src': ['self', 'wasm-unsafe-eval', bootstrapHash],
				'worker-src': ['self', 'blob:'],
				'style-src': ['self', 'unsafe-inline'],
				'img-src': ['self', 'blob:', 'data:'],
				'media-src': ['self', 'blob:'],
				'font-src': ['self', 'blob:', 'data:'],
				'connect-src': ['self'],
				'frame-src': ['self'],
				'object-src': ['none'],
				'base-uri': ['self'],
				'form-action': ['self']
			}
		}
	}
};

export default config;
