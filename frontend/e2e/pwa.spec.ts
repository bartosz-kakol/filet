import { expect, login, test } from './fixtures';

test('manifest, service worker and Web Share Target', async ({ page, filet, browserName }) => {
	test.skip(browserName !== 'chromium', 'share target and SW checks run on Chromium');
	const m = await (await page.request.get('/manifest.webmanifest')).json();
	expect(m.display).toBe('standalone');
	expect(m.share_target.action).toBe('/share-target');
	expect(m.share_target.params.files[0].name).toBe('files');

	await login(page.request);
	await page.goto('/p/media');
	await page.evaluate(() => navigator.serviceWorker.ready);
	await page.reload(); // controlled by the service worker from now on
	await page.evaluate(() => navigator.serviceWorker.ready);
	const target = await page.evaluate(async () => {
		const fd = new FormData();
		fd.append('files', new File(['shared one'], 'one.txt', { type: 'text/plain' }));
		fd.append('files', new File(['shared two'], 'two.txt', { type: 'text/plain' }));
		const r = await fetch('/share-target', { method: 'POST', body: fd });
		return r.url;
	});
	expect(target).toMatch(/\/share\?id=/);
	await page.goto(target);
	await expect(page.getByTestId('shared-files')).toContainText('one.txt');
	await page.getByRole('button', { name: 'Upload here' }).click();
	await expect.poll(() => filet.exists('media', '/two.txt')).toBe(true);
	expect(filet.read('media', '/one.txt').toString()).toBe('shared one');
});

test('a share-target POST without the service worker redirects', async ({ filet }) => {
	const r = await fetch(filet.url + '/share-target', { method: 'POST', redirect: 'manual' });
	expect(r.status).toBe(303);
	expect(r.headers.get('location')).toBe('/share?error=no-sw');
});
