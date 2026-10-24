import DOMPurify from 'dompurify';
import MarkdownIt from 'markdown-it';

export interface LinkContext {
	/** Directory of the markdown file, e.g. "/docs". */
	dir: string;
	rawUrl(path: string): string;
	previewHref(path: string): string;
	browseHref(path: string): string;
}

/** Resolves a relative link against dir; returns null for absolute URLs. */
export function resolveRelative(dir: string, href: string): string | null {
	if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//') || href.startsWith('#')) return null;
	const clean = href.split(/[?#]/)[0];
	if (!clean) return null;
	let decoded = clean;
	try {
		decoded = decodeURIComponent(clean);
	} catch {
		/* keep as is */
	}
	const parts = decoded.startsWith('/') ? [] : dir.split('/').filter(Boolean);
	for (const seg of decoded.split('/')) {
		if (!seg || seg === '.') continue;
		if (seg === '..') parts.pop();
		else parts.push(seg);
	}
	return '/' + parts.join('/') + (decoded.endsWith('/') && parts.length ? '/' : '');
}

/** Renders markdown with Filet's link rules, then sanitises it. */
export function renderMarkdown(src: string, ctx: LinkContext, purify = DOMPurify): string {
	const md = new MarkdownIt({ html: false, linkify: true, typographer: false });
	const defaultImage = md.renderer.rules.image!;
	md.renderer.rules.image = (tokens, idx, options, env, self) => {
		const t = tokens[idx];
		const src = t.attrGet('src') ?? '';
		if (/^https?:/i.test(src)) {
			// Remote images would leak the viewer's IP; the CSP blocks them anyway.
			t.attrSet('src', '');
		} else {
			const p = resolveRelative(ctx.dir, src);
			t.attrSet('src', p ? ctx.rawUrl(p.replace(/\/$/, '')) : '');
		}
		return defaultImage(tokens, idx, options, env, self);
	};
	const defaultLink =
		md.renderer.rules.link_open ?? ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
	md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
		const t = tokens[idx];
		const href = t.attrGet('href') ?? '';
		if (/^https?:/i.test(href)) {
			t.attrSet('target', '_blank');
			t.attrSet('rel', 'noopener noreferrer');
		} else if (href.startsWith('#')) {
			// in-page anchor: keep
		} else if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//')) {
			t.attrSet('href', ''); // other schemes are dropped
		} else {
			const p = resolveRelative(ctx.dir, href);
			if (!p) t.attrSet('href', '');
			else if (p.endsWith('/') || p === '/') t.attrSet('href', ctx.browseHref(p === '/' ? '/' : p.replace(/\/$/, '')));
			else t.attrSet('href', ctx.previewHref(p));
		}
		return defaultLink(tokens, idx, options, env, self);
	};
	const html = md.render(src);
	return purify.sanitize(html, { ADD_ATTR: ['target'] }) as string;
}
