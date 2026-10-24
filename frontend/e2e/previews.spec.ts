import { execFileSync } from 'node:child_process';
import { join } from 'node:path';
import { expect, login, test } from './fixtures';
import { hasFFmpeg, makeJpeg, makeVideo, minimalPdf, withExif } from './media';

test.beforeEach(async ({ page }) => {
	test.skip(!hasFFmpeg, 'ffmpeg is required for media fixtures');
	await login(page.request);
});

test('image: zoom, rotate and EXIF panel with camera model and GPS', async ({ page, filet, isMobile }) => {
	const jpg = makeJpeg(join(filet.dir, 'raw.jpg'));
	filet.write('media', '/photo.jpg', withExif(jpg, { make: 'Filet', model: 'TestCam 9000', lat: 52.2297, lon: 21.0122 }));
	await page.goto('/p/media?preview=photo.jpg');
	const img = page.getByTestId('preview-image');
	await expect(img).toBeVisible();
	const before = await img.getAttribute('style');
	await page.getByRole('button', { name: 'Zoom in' }).click();
	await expect(img).not.toHaveAttribute('style', before!);
	await page.getByRole('button', { name: 'Rotate right' }).click();
	await expect(img).toHaveAttribute('style', /rotate\(90deg\)/);
	await page.getByRole('button', { name: 'Info', exact: true }).click();
	const info = page.getByTestId('info-panel');
	await expect(info).toContainText('TestCam 9000');
	await expect(info).toContainText('52.229');
	await expect(info.getByRole('link', { name: 'Open in OpenStreetMap' })).toHaveAttribute('href', /mlat=52\.229/);
	void isMobile;
});

test('video: MP4 plays directly, MKV remuxes and seeks, DTS-only shows the audio banner', async ({ page, filet, browserName }) => {
	test.skip(browserName !== 'chromium', 'codec support differs per engine');
	makeVideo(join(filet.places.media, 'clip.mp4'));
	makeVideo(join(filet.places.media, 'clip.mkv'), { container: 'mkv', seconds: 20 });
	makeVideo(join(filet.places.media, 'dts.mkv'), { container: 'mkv', audio: 'dts' });

	await page.goto('/p/media?preview=clip.mp4');
	const video = page.getByTestId('video');
	await expect(video).not.toHaveAttribute('data-mode', 'remux');
	await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThan(0);

	await page.goto('/p/media?preview=clip.mkv');
	await expect(video).toHaveAttribute('data-mode', 'remux');
	await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.readyState)).toBeGreaterThan(0);
	const seek = page.getByRole('slider', { name: 'Seek' });
	await seek.evaluate((el: HTMLInputElement) => {
		el.value = '15';
		el.dispatchEvent(new Event('input', { bubbles: true }));
		el.dispatchEvent(new Event('change', { bubbles: true }));
	});
	await expect(page.getByTestId('media-time')).toContainText(/^0:1[4-9]/);

	await page.goto('/p/media?preview=dts.mkv');
	await expect(page.getByRole('status')).toContainText("This file's audio (DTS) can't be played in the browser");
});

test('text viewers: markdown toggle, YAML highlighting, large log, CSV', async ({ page, filet }) => {
	filet.write('media', '/docs/readme.md', '# Title\n\nHello **world**.\n');
	filet.write('media', '/docs/config.yaml', 'name: filet\nitems:\n  - a\n  - b\n');
	const lines: string[] = [];
	for (let i = 1; i <= 700_000; i++) lines.push(`2026-01-01 INFO entry ${i} ${'x'.repeat(50)}`);
	filet.write('media', '/docs/big.log', lines.join('\n') + '\n');
	filet.write('media', '/docs/t.csv', 'a,b\n"1, one",2\n');

	await page.goto('/p/media/docs?preview=readme.md');
	await expect(page.getByTestId('markdown').getByRole('heading', { name: 'Title' })).toBeVisible();
	await page.getByRole('button', { name: 'Raw' }).click();
	await expect(page.locator('.cm-content')).toContainText('# Title');
	await page.getByRole('button', { name: 'Rendered' }).click();
	await expect(page.getByTestId('markdown')).toBeVisible();

	await page.goto('/p/media/docs?preview=config.yaml');
	await expect(page.locator('.cm-lineNumbers')).toContainText('4');
	await expect(page.locator('.cm-content span').first()).toBeVisible(); // highlighted tokens are spans

	await page.goto('/p/media/docs?preview=big.log');
	await expect(page.getByTestId('large-text')).toBeVisible();
	await page.getByRole('button', { name: 'Jump to end' }).click();
	await expect(page.getByText('entry 700000 ')).toBeVisible();

	await page.goto('/p/media/docs?preview=t.csv');
	await expect(page.getByTestId('csv')).toContainText('1, one');

	// ".ts" that is TypeScript (not MPEG-TS) opens as code.
	filet.write('media', '/docs/app.ts', 'const answer: number = 42;\n');
	await page.goto('/p/media/docs?preview=app.ts');
	await expect(page.locator('.cm-content')).toContainText('const answer: number = 42;');
});

test('archives: tree and single-entry download', async ({ page, filet }) => {
	filet.write('media', '/src/a/hello.txt', 'hello from the archive');
	filet.write('media', '/src/b.md', '# b');
	execFileSync('zip', ['-qr', join(filet.places.media, 'pack.zip'), 'a', 'b.md'], { cwd: filet.path('media', '/src') });
	await page.goto('/p/media?preview=pack.zip');
	const tree = page.getByTestId('archive-tree');
	await expect(tree).toContainText('b.md');
	await tree.getByRole('button', { name: 'a', exact: true }).click();
	await expect(tree).toContainText('hello.txt');
	const dl = page.waitForEvent('download');
	await tree.getByRole('link', { name: 'Download hello.txt' }).click();
	const d = await dl;
	expect(d.suggestedFilename()).toBe('hello.txt');
	const body = (await (await page.request.get(await d.url())).body()).toString();
	expect(body).toBe('hello from the archive');
});

test('PDF opens in an iframe when the browser has a PDF viewer', async ({ page, filet }) => {
	filet.write('media', '/doc.pdf', minimalPdf());
	await page.goto('/p/media?preview=doc.pdf');
	const inline = await page.evaluate(() => (navigator as Navigator & { pdfViewerEnabled?: boolean }).pdfViewerEnabled === true);
	if (inline) await expect(page.locator('iframe[title="doc.pdf"]')).toHaveAttribute('src', /\/raw\/doc\.pdf$/);
	else await expect(page.getByRole('link', { name: 'Open', exact: true })).toBeVisible();
});

test('previous/next follows the sort order and the next image is prefetched', async ({ page, filet, isMobile }) => {
	for (let i = 0; i < 20; i++) {
		makeJpeg(join(filet.places.media, `img${String(i).padStart(2, '0')}.jpg`), i * 15, '320x240');
	}
	await page.goto('/p/media');
	const first = page.locator('[data-name="img00.jpg"]');
	if (isMobile) await first.tap();
	else await first.dblclick();
	await expect(page.getByTestId('preview-image')).toBeVisible();
	await expect(page.getByTestId('preview-counter')).toHaveText('1 / 20');
	await page.waitForTimeout(500); // prefetch of the neighbours
	const raws: string[] = [];
	page.on('request', (r) => r.url().includes('/raw/') && raws.push(r.url()));
	if (isMobile) {
		const box = (await page.getByRole('region', { name: 'Viewer' }).boundingBox())!;
		await page.touchscreen.tap(box.x + 5, box.y + 5);
		await page.dispatchEvent('[role=region][aria-label=Viewer]', 'touchstart', { touches: [{ identifier: 1, clientX: box.x + 300, clientY: box.y + 200 }] });
		await page.dispatchEvent('[role=region][aria-label=Viewer]', 'touchend', { changedTouches: [{ identifier: 1, clientX: box.x + 100, clientY: box.y + 205 }] });
	} else {
		await page.keyboard.press('ArrowRight');
	}
	await expect(page).toHaveURL(/preview=img01\.jpg/);
	await expect(page.getByTestId('preview-counter')).toHaveText('2 / 20');
	await expect(page.getByTestId('preview-image')).toHaveAttribute('src', /img01\.jpg/);
	expect(raws.filter((u) => u.includes('img01.jpg'))).toEqual([]);
	// Back closes the viewer (stepping used replaceState).
	await page.goBack();
	await expect(page.getByTestId('preview')).toHaveCount(0);
	await expect(page).toHaveURL(/\/p\/media$/);
});
