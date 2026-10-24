import { describe, expect, it } from 'vitest';
import { renderMarkdown, resolveRelative } from './markdown';

const ctx = {
	dir: '/docs',
	rawUrl: (p: string) => `/api/places/media/raw${p}`,
	previewHref: (p: string) => `/p/media${p.slice(0, p.lastIndexOf('/'))}?preview=${p.slice(p.lastIndexOf('/') + 1)}`,
	browseHref: (p: string) => `/p/media${p}`
};

describe('markdown rendering', () => {
	it('rewrites relative links and images', () => {
		const html = renderMarkdown('![i](img/a.png) [f](../notes.txt) [d](sub/) [w](https://example.com) [m](mailto:x@y) [h](#top)', ctx);
		expect(html).toContain('src="/api/places/media/raw/docs/img/a.png"');
		expect(html).toContain('href="/p/media?preview=notes.txt"');
		expect(html).toContain('href="/p/media/docs/sub"');
		expect(html).toMatch(/href="https:\/\/example.com" target="_blank" rel="noopener noreferrer"/);
		expect(html).not.toContain('mailto:');
		expect(html).toContain('href="#top"');
	});
	it('sanitises scripts and event handlers', () => {
		// html: false already escapes raw HTML; DOMPurify is the second line.
		const html = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))', ctx);
		expect(html).not.toMatch(/<script/i);
		expect(html).not.toMatch(/<img[^>]+onerror/i);
		expect(html).not.toContain('href="javascript:');
	});
	it('resolves paths', () => {
		expect(resolveRelative('/a/b', '../c/d.md')).toBe('/a/c/d.md');
		expect(resolveRelative('/a', '../../x')).toBe('/x');
		expect(resolveRelative('/a', '/abs.md')).toBe('/abs.md');
		expect(resolveRelative('/a', 'my%20file.md')).toBe('/a/my file.md');
		expect(resolveRelative('/a', 'https://x')).toBeNull();
	});
});
