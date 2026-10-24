import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { bytes, expect, login, openPlace, sha256, tempFile, test, uploadRow } from './fixtures';

test.beforeEach(async ({ page }) => login(page.request));

test('uploads 10 files and a nested folder', async ({ page, filet }) => {
	await openPlace(page);
	const files = Array.from({ length: 10 }, (_, i) => ({
		name: `file-${i}.bin`,
		mimeType: 'application/octet-stream',
		buffer: bytes(100_000 + i * 1000, i + 1)
	}));
	await page.getByTestId('file-input').setInputFiles(files);
	await expect(page.getByTestId('transfer-panel')).toBeVisible();
	for (const f of files) await expect(uploadRow(page, f.name)).toHaveAttribute('data-state', 'done');
	for (const f of files) expect(sha256(filet.read('media', '/' + f.name))).toBe(sha256(f.buffer));
	await expect(page.locator('[data-name="file-9.bin"]')).toBeVisible();

	// Folder upload (webkitdirectory) keeps the structure.
	const root = mkdtempSync(join(tmpdir(), 'filet-dir-'));
	mkdirSync(join(root, 'trip', 'raw'), { recursive: true });
	writeFileSync(join(root, 'trip', 'a.txt'), 'A');
	writeFileSync(join(root, 'trip', 'raw', 'b.txt'), 'B');
	await page.getByTestId('folder-input').setInputFiles(join(root, 'trip'));
	await expect(uploadRow(page, 'b.txt')).toHaveAttribute('data-state', 'done');
	await expect(uploadRow(page, 'a.txt')).toHaveAttribute('data-state', 'done');
	expect(filet.read('media', '/trip/raw/b.txt').toString()).toBe('B');
	expect(filet.read('media', '/trip/a.txt').toString()).toBe('A');
});

test('respects the parallel upload limit', async ({ page, filet }) => {
	await page.addInitScript(() => localStorage.setItem('filet.uploadConcurrency', '2'));
	await page.route('**/api/uploads/*/chunks/*', (route) =>
		route.continue({ headers: { ...route.request().headers(), 'x-filet-test-delay-ms': '400' } })
	);
	await openPlace(page);
	const files = Array.from({ length: 6 }, (_, i) => tempFile(`p${i}.bin`, bytes(9 * 1024 * 1024, i + 7)));
	await page.getByTestId('file-input').setInputFiles(files);
	let maxActive = 0;
	for (let i = 0; i < 40; i++) {
		const active = await page.locator('[data-testid=upload-row][data-state=hashing], [data-testid=upload-row][data-state=registering], [data-testid=upload-row][data-state=uploading]').count();
		maxActive = Math.max(maxActive, active);
		if ((await page.locator('[data-testid=upload-row][data-state=done]').count()) === 6) break;
		await page.waitForTimeout(250);
	}
	await expect(page.locator('[data-testid=upload-row][data-state=done]')).toHaveCount(6, { timeout: 60_000 });
	expect(maxActive).toBeGreaterThan(0);
	expect(maxActive).toBeLessThanOrEqual(2);
	void filet;
});
