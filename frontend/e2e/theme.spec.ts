import { expect, login, test } from './fixtures';

test('system theme follows the OS; explicit themes persist without a flash', async ({ page }) => {
	await login(page.request);
	await page.goto('/p/media');
	// "System" follows OS changes live, without a reload.
	await page.emulateMedia({ colorScheme: 'dark' });
	await expect(page.locator('html')).toHaveClass(/dark/);
	await page.emulateMedia({ colorScheme: 'light' });
	await expect(page.locator('html')).not.toHaveClass(/dark/);

	await page.getByTestId('app-menu').click();
	await page.getByRole('menuitemradio', { name: 'Dark' }).click();
	await page.keyboard.press('Escape');
	await expect(page.locator('html')).toHaveClass(/dark/);
	// The class is set by the inline bootstrap before any app code runs.
	await page.addInitScript(() => {
		document.addEventListener('DOMContentLoaded', () => {
			(window as unknown as { __early: boolean }).__early = document.documentElement.classList.contains('dark');
		});
	});
	await page.reload();
	expect(await page.evaluate(() => (window as unknown as { __early: boolean }).__early)).toBe(true);
	await page.getByTestId('app-menu').click();
	await page.getByRole('menuitemradio', { name: 'Light' }).click();
	await page.keyboard.press('Escape');
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.reload();
	await expect(page.locator('html')).not.toHaveClass(/dark/);
});
