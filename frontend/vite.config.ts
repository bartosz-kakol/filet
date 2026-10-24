import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { SvelteKitPWA } from '@vite-pwa/sveltekit';
import { defineConfig } from 'vitest/config';

const backend = 'http://127.0.0.1:8080';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit(),
		SvelteKitPWA({
			strategies: 'injectManifest',
			srcDir: 'src',
			filename: 'service-worker.ts',
			registerType: 'prompt',
			injectRegister: false,
			injectManifest: {
				globPatterns: ['client/**/*.{js,css,ico,png,svg,webp,woff2,wasm}'],
				maximumFileSizeToCacheInBytes: 8 * 1024 * 1024
			},
			manifest: {
				name: 'Filet',
				short_name: 'Filet',
				start_url: '/',
				scope: '/',
				display: 'standalone',
				background_color: '#09090b',
				theme_color: '#09090b',
				icons: [
					{ src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
					{ src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
					{ src: '/icons/maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' }
				],
				share_target: {
					action: '/share-target',
					method: 'POST',
					enctype: 'multipart/form-data',
					params: { title: 'title', text: 'text', url: 'url', files: [{ name: 'files', accept: ['*/*'] }] }
				}
			} as Record<string, unknown>,
			kit: { adapterFallback: 'index.html', spa: true },
			devOptions: { enabled: false }
		})
	],
	server: {
		proxy: {
			'/api': { target: backend, ws: false },
			'/stream': backend,
			'/healthz': backend
		}
	},
	worker: { format: 'es' },
	test: {
		include: ['src/**/*.test.ts'],
		environment: 'node',
		environmentMatchGlobs: [['src/**/*.dom.test.ts', 'jsdom']]
	}
});
