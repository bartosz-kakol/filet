import { expect, login, openPlace, test } from './fixtures';

test.beforeEach(async ({ page }) => login(page.request));

test('read-only place hides mutations; copying out works; the API refuses deletes', async ({ page, filet, isMobile }) => {
	filet.write('ro', '/keep.txt', 'ro');
	await openPlace(page, '/p/ro');
	await expect(page.getByText('Read-only').filter({ visible: true }).first()).toBeVisible();
	await expect(page.getByTestId('upload-menu')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'New folder' })).toHaveCount(0);
	await expect(page.getByTestId('fab')).toHaveCount(0);
	await page.locator('[data-name="keep.txt"]').click({ button: isMobile ? 'left' : 'right' });
	if (!isMobile) {
		await expect(page.getByRole('menuitem', { name: 'Rename' })).toHaveAttribute('data-disabled', '');
		await expect(page.getByRole('menuitem', { name: 'Delete' })).toHaveAttribute('data-disabled', '');
		await expect(page.getByRole('menuitem', { name: 'Move to…' })).toHaveAttribute('data-disabled', '');
		await page.keyboard.press('Escape');
	}
	const del = await page.request.post('/api/jobs', { data: { type: 'trash', place: 'ro', paths: ['/keep.txt'] } });
	expect(del.status()).toBe(403);
	expect((await del.json()).error.code).toBe('place_readonly');
	const copy = await page.request.post('/api/jobs', {
		data: { type: 'copy', src: { place: 'ro', paths: ['/keep.txt'] }, dst: { place: 'media', dir: '/' } }
	});
	expect(copy.status()).toBe(201);
	await expect.poll(() => filet.exists('media', '/keep.txt')).toBe(true);
});
