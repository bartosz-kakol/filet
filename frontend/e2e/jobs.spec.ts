import { readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { bytes, expect, login, openPlace, test } from './fixtures';

test.beforeEach(async ({ page, isMobile }) => {
	test.skip(isMobile, 'desktop interactions');
	await login(page.request);
});

function walk(dir: string): string[] {
	return readdirSync(dir).flatMap((n) => {
		const p = join(dir, n);
		return statSync(p).isDirectory() ? [p, ...walk(p)] : [p];
	});
}

test('copy a folder to another place through the destination picker', async ({ page, filet }) => {
	for (let i = 0; i < 5; i++) filet.write('media', `/album/p${i}.jpg`, bytes(200_000, i));
	await openPlace(page);
	await page.getByRole('button', { name: 'Actions for album' }).click();
	await page.getByRole('menuitem', { name: 'Copy to…' }).click();
	const dlg = page.getByRole('dialog');
	await dlg.getByRole('button', { name: 'Place' }).click();
	await page.getByRole('option', { name: 'Other' }).click();
	await dlg.getByRole('button', { name: 'Copy here' }).click();
	await expect(page.locator('[data-testid=job-row][data-status=completed]')).toHaveCount(1);
	await expect(page.getByTestId('job-row')).toContainText('Done');
	expect(filet.list('other', '/album').sort()).toEqual(['p0.jpg', 'p1.jpg', 'p2.jpg', 'p3.jpg', 'p4.jpg']);
});

test('cancelling a copy leaves no temporary files', async ({ page, filet }) => {
	for (let i = 0; i < 40; i++) filet.write('media', `/big/f${i}.bin`, bytes(1024 * 1024, i + 1));
	await filet.copyDelay(250);
	await openPlace(page);
	const r = await page.request.post('/api/jobs', {
		data: { type: 'copy', src: { place: 'media', paths: ['/big'] }, dst: { place: 'other', dir: '/' } }
	});
	expect(r.status()).toBe(201);
	const row = page.getByTestId('job-row');
	await expect(row).toBeVisible();
	await row.getByRole('button', { name: 'Cancel operation' }).click();
	await expect(row).toHaveAttribute('data-status', 'cancelled');
	await expect(row).toContainText(/Cancelled — \d+ of \d+ items done/);
	expect(walk(filet.places.other).filter((p) => p.includes('.filet-copy-'))).toEqual([]);
});

test('drag onto a breadcrumb moves; Ctrl-drop copies', async ({ page, filet }) => {
	filet.write('media', '/A/move-me.txt', 'm');
	filet.write('media', '/A/copy-me.txt', 'c');
	await openPlace(page, '/p/media/A');
	const crumb = page.getByRole('navigation', { name: 'Breadcrumbs' }).getByRole('link', { name: 'Media' });
	await page.locator('[data-name="move-me.txt"]').dragTo(crumb);
	await expect.poll(() => filet.exists('media', '/move-me.txt')).toBe(true);
	expect(filet.exists('media', '/A/move-me.txt')).toBe(false);

	// Alt/Option (or Ctrl) makes the drop a copy; Ctrl+click is a context
	// menu on macOS, so the test uses Alt.
	await page.keyboard.down('Alt');
	await page.locator('[data-name="copy-me.txt"]').dragTo(crumb);
	await page.keyboard.up('Alt');
	await expect.poll(() => filet.exists('media', '/copy-me.txt')).toBe(true);
	expect(filet.exists('media', '/A/copy-me.txt')).toBe(true);
});

test('delete to trash, restore, then delete permanently', async ({ page, filet }) => {
	filet.write('media', '/docs/report.pdf', 'pdf');
	await openPlace(page, '/p/media/docs');
	await page.locator('[data-name="report.pdf"]').click();
	await page.keyboard.press('Delete');
	await page.getByRole('button', { name: 'Move to Trash' }).click();
	await expect.poll(() => filet.exists('media', '/docs/report.pdf')).toBe(false);

	await page.goto('/trash/media');
	await expect(page.getByTestId('trash-row')).toHaveCount(1);
	await expect(page.getByTestId('trash-row')).toContainText('/docs/report.pdf');
	await page.getByTestId('trash-row').click();
	await page.getByRole('button', { name: 'Restore' }).click();
	await expect.poll(() => filet.exists('media', '/docs/report.pdf')).toBe(true);
	await expect(page.getByTestId('trash-row')).toHaveCount(0);

	// Trash it again via the API and purge from the Trash view.
	await page.request.post('/api/jobs', { data: { type: 'trash', place: 'media', paths: ['/docs/report.pdf'] } });
	await expect.poll(() => filet.exists('media', '/docs/report.pdf')).toBe(false);
	await page.reload();
	await page.getByTestId('trash-row').click();
	await page.getByRole('button', { name: 'Delete permanently' }).first().click();
	await page.getByRole('dialog').getByRole('button', { name: 'Delete permanently' }).click();
	await expect(page.getByTestId('trash-row')).toHaveCount(0);
	expect(readdirSync(join(filet.places.media, '.filet', 'trash'))).toEqual([]);
});
