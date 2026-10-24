import { expect, login, test } from './fixtures';

test('external player link supports Range and expires after the TTL', async ({ page, filet, playwright }) => {
	await login(page.request);
	filet.write('media', '/movie.mp4', Buffer.alloc(4096, 7));
	const r = await page.request.post('/api/stream-links', { data: { place: 'media', path: '/movie.mp4' } });
	expect(r.status()).toBe(201);
	const { url } = await r.json();
	expect(url).toMatch(/^\/stream\/[^/]+\/movie\.mp4$/);
	const anon = await playwright.request.newContext({ baseURL: filet.url });
	const part = await anon.get(url, { headers: { Range: 'bytes=0-99' } });
	expect(part.status()).toBe(206);
	expect((await part.body()).length).toBe(100);
	await filet.advanceClock(2 * 60_000); // TTL is 1 minute in the test config
	expect((await anon.get(url)).status()).toBe(404);
	await anon.dispose();
});

test('password-protected folder share: unlock, browse, preview, zip, revoke, expiry', async ({ page, filet, browser, isMobile }) => {
	await login(page.request);
	filet.write('media', '/trip/day1.txt', 'day one');
	filet.write('media', '/trip/sub/day2.txt', 'day two');
	filet.write('media', '/trip/.private', 'hidden');

	// Create the link through the UI.
	await page.goto('/p/media');
	await page.locator('[data-name=trip]').getByRole('button', { name: 'Actions for trip' }).click();
	await page.getByRole('menuitem', { name: 'Share link…' }).click();
	const dlg = page.getByRole('dialog');
	await dlg.getByRole('combobox').selectOption('1 day');
	await dlg.getByLabel('Password (optional)').fill('s3cret');
	await dlg.getByRole('button', { name: 'Create link' }).click();
	const shareUrl = await dlg.getByTestId('share-url').inputValue();
	expect(shareUrl).toMatch(/\/s\/[A-Za-z0-9_-]{32}$/);

	const ctx = await browser.newContext(isMobile ? { hasTouch: true, isMobile: true, viewport: { width: 412, height: 839 } } : {});
	const pub = await ctx.newPage();
	await pub.goto(shareUrl);
	await pub.getByLabel('Password').fill('nope');
	await pub.getByRole('button', { name: 'Unlock' }).click();
	await expect(pub.getByText('Incorrect password')).toBeVisible();
	await pub.waitForTimeout(2100); // per-link lockout (2 s in the test config)
	await pub.getByLabel('Password').fill('s3cret');
	await pub.getByRole('button', { name: 'Unlock' }).click();
	await expect(pub.locator('[data-name="day1.txt"]')).toBeVisible();
	await expect(pub.locator('[data-name=".private"]')).toHaveCount(0);
	await expect(pub.getByText('Available until')).toBeVisible();
	const open = (sel: string) => (isMobile ? pub.locator(sel).tap() : pub.locator(sel).dblclick());
	await open('[data-name=sub]');
	await expect(pub.locator('[data-name="day2.txt"]')).toBeVisible();
	await open('[data-name="day2.txt"]');
	await expect(pub.locator('.cm-content')).toContainText('day two');
	await pub.keyboard.press('Escape');
	await pub.goto(shareUrl);
	const dl = pub.waitForEvent('download');
	await pub.getByRole('button', { name: 'Download all' }).click();
	expect((await dl).suggestedFilename()).toBe('trip.zip');

	// Revoke from the Links view.
	await page.goto('/links');
	await page.getByTestId('link-row').getByRole('button', { name: 'Revoke' }).click();
	await page.getByRole('dialog').getByRole('button', { name: 'Revoke' }).click();
	await expect(page.getByTestId('link-row')).toHaveCount(0);
	await pub.reload();
	await expect(pub.getByText('This link is invalid or has expired.')).toBeVisible();

	// An expired link.
	const r = await page.request.post('/api/share-links', {
		data: { place: 'media', path: '/trip', expiresAt: Date.now() + 3_600_000 }
	});
	const link = await r.json();
	await filet.advanceClock(2 * 3_600_000);
	await pub.goto(link.url);
	await expect(pub.getByText('This link is invalid or has expired.')).toBeVisible();
	await ctx.close();
});
