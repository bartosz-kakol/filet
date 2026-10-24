import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync, readdirSync, realpathSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';

export const USER = 'admin';
export const PASS = 'e2e password 123';
const BIN = resolve(process.env.FILET_BIN ?? '../bin/filet-test');

function freePort(): Promise<number> {
	return new Promise((res, rej) => {
		const s = createServer();
		s.listen(0, '127.0.0.1', () => {
			const p = (s.address() as { port: number }).port;
			s.close(() => res(p));
		});
		s.on('error', rej);
	});
}

export interface ServerOptions {
	/** Extra YAML appended to the generated config. */
	extra?: string;
}

/** A Filet process with its own temp directory, places and database. */
export class Filet {
	dir: string;
	url = '';
	port = 0;
	proc: ChildProcess | null = null;
	log = '';
	places: Record<string, string>;

	constructor(private opts: ServerOptions = {}) {
		this.dir = realpathSync(mkdtempSync(join(tmpdir(), 'filet-e2e-')));
		this.places = { media: join(this.dir, 'media'), other: join(this.dir, 'other'), ro: join(this.dir, 'ro') };
		for (const p of Object.values(this.places)) mkdirSync(p, { recursive: true });
		mkdirSync(join(this.dir, 'data'));
	}

	async start() {
		if (!this.port) this.port = await freePort();
		this.url = `http://127.0.0.1:${this.port}`;
		const cfg = join(this.dir, 'config.yaml');
		writeFileSync(
			cfg,
			[
				`listen: "127.0.0.1:${this.port}"`,
				`database: ${join(this.dir, 'data', 'filet.db')}`,
				'places:',
				`  - name: Media`,
				`    path: ${this.places.media}`,
				`  - name: Other`,
				`    path: ${this.places.other}`,
				`  - name: RO`,
				`    path: ${this.places.ro}`,
				`    readonly: true`,
				'auth:',
				'  secure_cookies: false',
				'  login_lockout:',
				'    duration: 2s',
				'stream_links:',
				'  ttl: 1m',
				'smart:',
				'  enabled: false',
				this.opts.extra ?? ''
			].join('\n')
		);
		this.proc = spawn(BIN, ['--config', cfg], {
			env: { ...process.env, FILET_USERNAME: USER, FILET_PASSWORD: PASS },
			stdio: ['ignore', 'pipe', 'pipe']
		});
		this.proc.stderr!.on('data', (d) => (this.log += d.toString()));
		this.proc.stdout!.on('data', (d) => (this.log += d.toString()));
		const deadline = Date.now() + 15_000;
		while (Date.now() < deadline) {
			try {
				const r = await fetch(this.url + '/healthz');
				if (r.ok) return;
			} catch {
				/* not up yet */
			}
			await new Promise((r) => setTimeout(r, 100));
		}
		throw new Error('Filet did not start:\n' + this.log);
	}

	/** Simulates a crash (SIGKILL). */
	kill() {
		this.proc?.kill('SIGKILL');
		this.proc = null;
	}

	async restart() {
		this.kill();
		await new Promise((r) => setTimeout(r, 200));
		await this.start();
	}

	stop() {
		this.proc?.kill('SIGTERM');
		this.proc = null;
		try {
			rmSync(this.dir, { recursive: true, force: true });
		} catch {
			/* best effort */
		}
	}

	path(place: string, rel: string) {
		return join(this.places[place], rel.replace(/^\//, ''));
	}

	write(place: string, rel: string, content: string | Buffer) {
		const p = this.path(place, rel);
		mkdirSync(dirname(p), { recursive: true });
		writeFileSync(p, content);
	}

	exists(place: string, rel: string) {
		return existsSync(this.path(place, rel));
	}

	read(place: string, rel: string) {
		return readFileSync(this.path(place, rel));
	}

	list(place: string, rel = '/') {
		return readdirSync(this.path(place, rel));
	}

	/** Test-only server controls (filettest build). */
	async advanceClock(ms: number) {
		await fetch(this.url + '/__test/clock', { method: 'POST', body: JSON.stringify({ advanceMs: ms }) });
	}
	async fakeFree(bytes: number) {
		await fetch(this.url + '/__test/statfs', { method: 'POST', body: JSON.stringify({ free: bytes }) });
	}
	async copyDelay(ms: number) {
		await fetch(this.url + '/__test/copydelay', { method: 'POST', body: JSON.stringify({ ms }) });
	}
	async requests(clear = false): Promise<string[]> {
		return (await fetch(this.url + `/__test/requests${clear ? '?clear=1' : ''}`)).json();
	}
}

export const test = base.extend<{ filet: Filet; serverOptions: ServerOptions; pageErrors: string[] }>({
	serverOptions: [{}, { option: true }],
	// Any uncaught exception in the page fails the test.
	pageErrors: [
		async ({ page }, use) => {
			const errors: string[] = [];
			page.on('pageerror', (e) => {
				// WebKit reports this benign layout notice as an error event.
				if (e.message.startsWith('ResizeObserver loop')) return;
				errors.push(e.message + '\n' + (e.stack ?? ''));
			});
			await use(errors);
			expect(errors, 'uncaught page errors').toEqual([]);
		},
		{ auto: true }
	],
	filet: async ({ serverOptions }, use) => {
		const s = new Filet(serverOptions);
		await s.start();
		await use(s);
		s.stop();
	},
	baseURL: async ({ filet }, use) => use(filet.url)
});

export { expect };

/** Signs in through the API (shares the cookie jar with the page). */
export async function login(request: APIRequestContext) {
	const r = await request.post('/api/auth/login', { data: { username: USER, password: PASS } });
	expect(r.status()).toBe(204);
}

export async function openPlace(page: Page, path = '/p/media') {
	await page.goto(path);
	await expect(page.locator('[data-testid=entry]').first().or(page.getByText('This folder is empty'))).toBeVisible();
}

export function sha256(buf: Buffer | Uint8Array) {
	return createHash('sha256').update(buf).digest('hex');
}

/** Deterministic pseudo-random content. */
export function bytes(n: number, seed = 1): Buffer {
	const b = Buffer.allocUnsafe(n);
	let x = seed >>> 0 || 1;
	for (let i = 0; i < n; i++) {
		x ^= x << 13;
		x ^= x >>> 17;
		x ^= x << 5;
		b[i] = x & 0xff;
	}
	return b;
}

/** Writes a temp file for setInputFiles (large uploads stream from disk). */
export function tempFile(name: string, content: Buffer): string {
	const d = mkdtempSync(join(tmpdir(), 'filet-up-'));
	const p = join(d, name);
	writeFileSync(p, content);
	return p;
}

/** Adds a header to every chunk PUT (fault injection, filettest build). */
export async function chunkHeaders(page: Page, headers: (index: number, attempt: number) => Record<string, string>) {
	const attempts = new Map<string, number>();
	await page.route('**/api/uploads/*/chunks/*', async (route) => {
		const url = route.request().url();
		const n = (attempts.get(url) ?? 0) + 1;
		attempts.set(url, n);
		const idx = Number(url.split('/').pop());
		await route.continue({ headers: { ...route.request().headers(), ...headers(idx, n) } });
	});
}

export function uploadRow(page: Page, name: string) {
	return page.locator('[data-testid=upload-row]', { hasText: name });
}
