import { expect, login, test } from './fixtures';

test('mobile: FAB, bottom-sheet place chooser, long-press selection, collapsible panel', async ({ page, filet, isMobile }) => {
	test.skip(!isMobile, 'mobile layout only');
	filet.write('media', '/a.txt', 'a');
	filet.write('media', '/b.txt', 'b');
	await login(page.request);
	await page.goto('/p/media');
	await expect(page.getByTestId('fab')).toBeVisible();

	await page.getByRole('button', { name: 'Choose place' }).tap();
	const sheet = page.getByRole('dialog');
	await expect(sheet).toBeVisible();
	await expect(sheet.getByRole('option', { name: /^Other/ })).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(sheet).toHaveCount(0);

	// Long-press enters selection mode; further taps toggle.
	const row = page.locator('[data-name="a.txt"]');
	await row.dispatchEvent('pointerdown', { pointerType: 'touch', isPrimary: true });
	await page.waitForTimeout(650);
	await row.dispatchEvent('pointerup', { pointerType: 'touch', isPrimary: true });
	await row.dispatchEvent('click');
	await expect(page.getByTestId('selection-bar')).toContainText('1 selected');
	await page.locator('[data-name="b.txt"]').dispatchEvent('pointerdown', { pointerType: 'touch', isPrimary: true });
	await page.locator('[data-name="b.txt"]').dispatchEvent('pointerup', { pointerType: 'touch', isPrimary: true });
	await page.locator('[data-name="b.txt"]').dispatchEvent('click');
	await expect(page.getByTestId('selection-bar')).toContainText('2 selected');
	await page.getByRole('button', { name: 'Clear selection' }).tap();

	// The upload panel collapses to its bar.
	await page.getByTestId('fab').tap();
	await page.getByTestId('file-input').setInputFiles({ name: 'm.txt', mimeType: 'text/plain', buffer: Buffer.from('m') });
	await page.keyboard.press('Escape');
	const panel = page.getByTestId('transfer-panel');
	await expect(panel).toBeVisible();
	await expect(panel.locator('[data-testid=upload-row][data-state=done]')).toHaveCount(1);
	await panel.getByRole('button', { name: 'Collapse' }).tap();
	await expect(panel.getByTestId('upload-row')).toHaveCount(0);
	await panel.getByRole('button', { name: 'Expand' }).tap();
	await expect(panel.getByTestId('upload-row')).toHaveCount(1);
});
