import { readdirSync } from 'node:fs';
import { bytes, chunkHeaders, expect, login, openPlace, sha256, tempFile, test, uploadRow } from './fixtures';

const MiB = 1024 * 1024;

test.beforeEach(async ({ page, isMobile }) => {
	test.skip(isMobile, 'covered on desktop');
	await login(page.request);
});

test('a corrupted chunk is re-sent; three failures mark the file corrupted', async ({ page, filet }) => {
	// First file: chunk 1 is corrupted once in transit.
	await chunkHeaders(page, (idx, attempt) => (idx === 1 && attempt === 1 ? { 'x-filet-test-corrupt-chunk': '1' } : {}));
	await openPlace(page);
	const data = bytes(20 * MiB, 3);
	await page.getByTestId('file-input').setInputFiles(tempFile('once.bin', data));
	await expect(uploadRow(page, 'once.bin')).toHaveAttribute('data-state', 'done');
	expect(sha256(filet.read('media', '/once.bin'))).toBe(sha256(data));

	// Second file: chunk 0 is corrupted on every attempt.
	await page.unroute('**/api/uploads/*/chunks/*');
	await chunkHeaders(page, (idx) => (idx === 0 ? { 'x-filet-test-corrupt-chunk': '1' } : {}));
	await page.getByTestId('file-input').setInputFiles(tempFile('bad.bin', bytes(10 * MiB, 4)));
	await expect(uploadRow(page, 'bad.bin')).toHaveAttribute('data-state', 'corrupted');
	await expect(uploadRow(page, 'bad.bin')).toContainText('Corrupted');
	expect(readdirSync(filet.places.media).filter((n) => n.startsWith('.filet-part-'))).toEqual([]);
	const left = await page.request.get('/api/uploads/leftovers');
	await filet.advanceClock(3 * 60_000);
	expect(await (await page.request.get('/api/uploads/leftovers')).json()).toEqual([]);
	void left;
});

async function progressOf(page: import('@playwright/test').Page, name: string): Promise<number> {
	const t = (await uploadRow(page, name).textContent()) ?? '';
	const m = t.match(/Uploading (\d+) %/);
	return m ? Number(m[1]) : 0;
}

test('resumes after a page reload without re-sending verified data', async ({ page, filet }) => {
	const data = bytes(200 * MiB, 5);
	const file = tempFile('big.bin', data);
	await page.route('**/api/uploads/*/chunks/*', (route) =>
		route.continue({ headers: { ...route.request().headers(), 'x-filet-test-delay-ms': '150' } })
	);
	await openPlace(page);
	await page.getByTestId('file-input').setInputFiles(file);
	await expect.poll(() => progressOf(page, 'big.bin'), { timeout: 60_000 }).toBeGreaterThanOrEqual(35);
	page.on('dialog', (d) => d.accept()); // beforeunload
	await page.unroute('**/api/uploads/*/chunks/*');
	await page.reload();
	// After 120 s of inactivity the upload is a leftover.
	await filet.advanceClock(3 * 60_000);
	await page.reload();
	await expect(page.getByTestId('leftover-badge')).toHaveText('1');
	await page.getByTestId('file-input').setInputFiles(file);
	await expect(uploadRow(page, 'big.bin')).toContainText(/Resumed — \d+ % was already on the server/);
	await expect(uploadRow(page, 'big.bin')).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
	expect(sha256(filet.read('media', '/big.bin'))).toBe(sha256(data));
});

test('survives a server crash and continues without re-sending acknowledged chunks', async ({ page, filet }) => {
	const data = bytes(120 * MiB, 6);
	await page.route('**/api/uploads/*/chunks/*', (route) =>
		route.continue({ headers: { ...route.request().headers(), 'x-filet-test-delay-ms': '200' } }).catch(() => {})
	);
	await openPlace(page);
	await page.getByTestId('file-input').setInputFiles(tempFile('crash.bin', data));
	await expect.poll(() => progressOf(page, 'crash.bin'), { timeout: 60_000 }).toBeGreaterThanOrEqual(25);
	const before = (await filet.requests()).filter((r) => r.startsWith('PUT') && r.includes('/chunks/'));
	const id = before[0].split('/')[3];
	filet.kill();
	await expect(uploadRow(page, 'crash.bin')).toContainText('Waiting for connection…', { timeout: 30_000 });
	await filet.start();
	await filet.requests(true);
	// What the server holds right after restart.
	const state = await (await page.request.get(`/api/uploads/${id}`)).json();
	const missing = (state.missing as [number, number][]).reduce((n, [a, b]) => n + b - a + 1, 0);
	await expect(uploadRow(page, 'crash.bin')).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
	const after = (await filet.requests()).filter((r) => r.startsWith('PUT') && r.includes('/chunks/'));
	// Only missing chunks plus at most the re-sent final chunk.
	expect(after.length).toBeLessThanOrEqual(missing + 1);
	expect(sha256(filet.read('media', '/crash.bin'))).toBe(sha256(data));
});

test('resumes from the Leftovers view in a fresh browser context', async ({ page, filet, browser }) => {
	const data = bytes(60 * MiB, 7);
	const file = tempFile('left.bin', data);
	await page.route('**/api/uploads/*/chunks/*', (route) =>
		route.continue({ headers: { ...route.request().headers(), 'x-filet-test-delay-ms': '200' } }).catch(() => {})
	);
	await openPlace(page);
	await page.getByTestId('file-input').setInputFiles(file);
	await expect.poll(() => progressOf(page, 'left.bin'), { timeout: 60_000 }).toBeGreaterThanOrEqual(20);
	await page.context().close();
	await filet.advanceClock(3 * 60_000);

	const ctx = await browser.newContext({ baseURL: filet.url });
	const p2 = await ctx.newPage();
	await login(p2.request);
	await p2.goto('/leftovers');
	await expect(p2.getByTestId('leftover-row')).toHaveCount(1);
	// A different file is refused.
	await p2.getByTestId('resume-input').evaluate((el) => el.removeAttribute('class'));
	await p2.getByRole('button', { name: 'Resume' }).click();
	await p2.getByTestId('resume-input').setInputFiles(tempFile('left.bin', bytes(1000, 1)));
	await expect(p2.getByText('This is not the same file (name or size differs)')).toBeVisible();
	await p2.getByRole('button', { name: 'Resume' }).click();
	await p2.getByTestId('resume-input').setInputFiles(file);
	await expect(uploadRow(p2, 'left.bin')).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
	expect(sha256(filet.read('media', '/left.bin'))).toBe(sha256(data));
	await ctx.close();
});

test('waits for the network and resumes when it is back', async ({ page, context, filet }) => {
	const data = bytes(60 * MiB, 8);
	await page.route('**/api/uploads/*/chunks/*', (route) =>
		route.continue({ headers: { ...route.request().headers(), 'x-filet-test-delay-ms': '150' } }).catch(() => {})
	);
	await openPlace(page);
	await page.getByTestId('file-input').setInputFiles(tempFile('net.bin', data));
	await expect.poll(() => progressOf(page, 'net.bin'), { timeout: 60_000 }).toBeGreaterThanOrEqual(10);
	await context.setOffline(true);
	await expect(uploadRow(page, 'net.bin')).toContainText('Waiting for connection…', { timeout: 30_000 });
	await context.setOffline(false);
	await expect(uploadRow(page, 'net.bin')).toHaveAttribute('data-state', 'done', { timeout: 90_000 });
	expect(sha256(filet.read('media', '/net.bin'))).toBe(sha256(data));
});

test('conflicts: overwrite, keep both, skip and apply to all', async ({ page, filet }) => {
	for (const n of ['a.txt', 'b.txt', 'c.txt', 'd.txt', 'e.txt', 'f.txt']) filet.write('media', '/' + n, 'old ' + n);
	await openPlace(page);
	const up = (names: string[]) =>
		page.getByTestId('file-input').setInputFiles(names.map((n) => ({ name: n, mimeType: 'text/plain', buffer: Buffer.from('new ' + n) })));
	const dialog = page.getByRole('dialog');

	await up(['a.txt']);
	await expect(dialog).toContainText('“a.txt” already exists in /');
	await dialog.getByRole('button', { name: /Overwrite/ }).click();
	await expect(uploadRow(page, 'a.txt')).toHaveAttribute('data-state', 'done');
	expect(filet.read('media', '/a.txt').toString()).toBe('new a.txt');
	const trash = await (await page.request.get('/api/places/media/trash')).json();
	expect(trash.map((t: { originalPath: string }) => t.originalPath)).toContain('/a.txt');

	await up(['b.txt']);
	await expect(dialog).toContainText('Saves as b (1).txt');
	await dialog.getByRole('button', { name: /Keep both/ }).click();
	await expect(uploadRow(page, 'b (1).txt')).toHaveAttribute('data-state', 'done');
	expect(filet.read('media', '/b (1).txt').toString()).toBe('new b.txt');
	expect(filet.read('media', '/b.txt').toString()).toBe('old b.txt');

	await up(['c.txt']);
	await dialog.getByRole('button', { name: 'Skip' }).click();
	await expect(uploadRow(page, 'c.txt')).toHaveAttribute('data-state', 'skipped');
	expect(filet.read('media', '/c.txt').toString()).toBe('old c.txt');

	await up(['d.txt', 'e.txt', 'f.txt']);
	await expect(dialog).toContainText('Apply to all remaining conflicts (3)');
	await dialog.getByRole('checkbox').click();
	await dialog.getByRole('button', { name: /Keep both/ }).click();
	for (const n of ['d (1).txt', 'e (1).txt', 'f (1).txt']) await expect(uploadRow(page, n)).toHaveAttribute('data-state', 'done');
	await expect(dialog).toHaveCount(0);
});

test('rejects an oversized file before hashing', async ({ page, filet }) => {
	await filet.fakeFree(MiB);
	await openPlace(page);
	await page.getByTestId('file-input').setInputFiles(tempFile('huge.bin', bytes(5 * MiB, 9)));
	const row = uploadRow(page, 'huge.bin');
	await expect(row).toHaveAttribute('data-state', 'failed');
	await expect(row).toContainText('Not enough space on Media: needs 5.24 MB, 1.05 MB free');
	const reqs = await filet.requests();
	expect(reqs.some((r) => r === 'POST /api/uploads')).toBe(false);
	await filet.fakeFree(-1);
});
