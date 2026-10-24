import { chmodSync } from 'node:fs';
import { expect, login, openPlace, test } from './fixtures';

test.beforeEach(async ({ page }) => login(page.request));

test('view mode is remembered per directory and not inherited', async ({ page, filet }) => {
	filet.write('media', '/A/B/inner.txt', 'x');
	filet.write('media', '/A/file.txt', 'x');
	await openPlace(page, '/p/media/A');
	await page.getByRole('radio', { name: 'Grid view' }).click();
	await expect(page.getByRole('heading', { name: 'Folders' })).toBeVisible();
	await page.goto('/p/media/A/B');
	await expect(page.getByRole('radio', { name: 'List view' })).toHaveAttribute('data-state', 'on');
	await page.goto('/p/media/A');
	await expect(page.getByRole('radio', { name: 'Grid view' })).toHaveAttribute('data-state', 'on');
});

test('sorting, hidden files, permission indicator and disk usage', async ({ page, filet, isMobile }) => {
	filet.write('media', '/small.txt', 'a');
	filet.write('media', '/big.txt', 'a'.repeat(5000));
	filet.write('media', '/mid.txt', 'a'.repeat(100));
	filet.write('media', '/.secret', 'hidden');
	filet.write('media', '/locked/inside.txt', 'x');
	chmodSync(filet.path('media', '/locked'), 0o000);
	try {
		await openPlace(page);
		const names = () => page.locator('[data-testid=entry]').evaluateAll((els) => els.map((e) => e.getAttribute('data-name')));
		expect(await names()).toEqual(['locked', 'big.txt', 'mid.txt', 'small.txt']);
		// Sort by size via the sort menu.
		await page.getByRole('button', { name: 'Sort' }).click();
		await page.getByRole('menuitem', { name: 'Size' }).click();
		expect(await names()).toEqual(['locked', 'small.txt', 'mid.txt', 'big.txt']);
		await page.getByRole('button', { name: 'Sort' }).click();
		await page.getByRole('menuitem', { name: 'Size' }).click();
		expect(await names()).toEqual(['locked', 'big.txt', 'mid.txt', 'small.txt']);
		// Dotfiles appear with the toggle.
		await expect(page.locator('[data-name=".secret"]')).toHaveCount(0);
		await page.getByTestId('app-menu').click();
		await page.getByRole('menuitemcheckbox', { name: 'Show hidden files' }).click();
		await page.keyboard.press('Escape');
		await expect(page.locator('[data-name=".secret"]')).toHaveCount(1);
		// The unreadable folder carries the permission indicator.
		if (process.getuid?.() !== 0) await expect(page.locator('[data-name=locked] [aria-label="Permission denied"]')).toBeVisible();
		// Place chooser shows the usage bar text.
		await page.getByRole('button', { name: 'Choose place' }).click();
		await expect(page.getByText(/free of/).first()).toBeVisible();
		void isMobile;
	} finally {
		chmodSync(filet.path('media', '/locked'), 0o755);
	}
});

test('a plain click selects one item; multi-select mode needs two via a modifier', async ({ page, filet, isMobile }) => {
	test.skip(isMobile, 'desktop interactions');
	for (const n of ['a.txt', 'b.txt', 'c.txt', 'd.txt']) filet.write('media', `/${n}`, n);
	await openPlace(page);
	const bar = page.getByTestId('selection-bar');
	const checks = page.getByRole('button', { name: /^Select / });
	await page.locator('[data-name="a.txt"]').click();
	await expect(page.locator('[data-name="a.txt"]')).toHaveAttribute('aria-selected', 'true');
	await expect(bar).toHaveCount(0);
	await expect(checks.first()).toBeHidden();
	await page.locator('[data-name="b.txt"]').click({ modifiers: ['ControlOrMeta'] });
	await expect(bar).toContainText('2 selected');
	await expect(checks.first()).toBeVisible();
	await page.locator('[data-name="d.txt"]').click({ modifiers: ['Shift'] });
	await expect(bar).toContainText('3 selected');
	// A plain click leaves multi-select mode.
	await page.locator('[data-name="c.txt"]').click();
	await expect(bar).toHaveCount(0);
	await expect(page.locator('[aria-selected=true]')).toHaveCount(1);
});

test('releasing the right-click that opens the context menu does not pick an item', async ({ page, filet, isMobile }) => {
	test.skip(isMobile, 'desktop interactions');
	filet.write('media', '/a.txt', 'a');
	await openPlace(page);
	await page.locator('[data-name="a.txt"]').click({ button: 'right' });
	const menu = page.getByRole('menu');
	await expect(menu).toBeVisible();
	await page.waitForTimeout(300);
	await expect(menu).toBeVisible();
	expect(page.url()).not.toContain('preview=');
	await menu.getByRole('menuitem', { name: 'Rename' }).click();
	await expect(page.getByRole('dialog')).toContainText('Rename');
});
