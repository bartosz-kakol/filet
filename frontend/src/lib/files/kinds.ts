// Mirror of backend/internal/places/kinds.go. kinds.test.ts checks both
// against backend/internal/places/testdata/kinds.json.
import type { Kind } from '$lib/api/types';

const table: Record<string, Kind> = {};
const add = (kind: Kind, ...exts: string[]) => exts.forEach((e) => (table[e] = kind));
export const RAW_EXTS = ['dng', 'cr2', 'cr3', 'nef', 'arw', 'raf', 'orf', 'rw2', 'pef', 'srw'];

add('image', 'jpg', 'jpeg', 'jfif', 'png', 'gif', 'webp', 'avif', 'bmp', 'ico', 'svg');
add('image-render', 'heic', 'heif', 'tif', 'tiff', ...RAW_EXTS);
add('video', 'mp4', 'm4v', 'mov', 'webm', 'mkv', 'avi', 'wmv', 'flv', 'ts', 'm2ts', 'mts', 'mpg', 'mpeg', '3gp', 'ogv');
add('audio', 'mp3', 'm4a', 'aac', 'flac', 'wav', 'ogg', 'oga', 'opus', 'wma', 'aiff', 'aif', 'ape', 'wv', 'mka');
add('markdown', 'md', 'markdown', 'mdown');
add(
	'text',
	...'txt log json jsonc yaml yml toml ini conf cfg env properties xml html htm css scss less js mjs cjs tsx jsx svelte vue go rs py rb php java kt kts c h cpp cc hpp cs swift sh bash zsh fish ps1 bat sql lua pl r dart nix tf hcl gradle proto graphql diff patch srt vtt'.split(
		' '
	)
);
add('csv', 'csv', 'tsv');
add('pdf', 'pdf');
add('archive', 'zip', 'tar', 'tar.gz', 'tgz', '7z');
add('font', 'ttf', 'otf', 'woff', 'woff2');

export const KIND_TABLE: Readonly<Record<string, Kind>> = table;
export const TEXT_FILENAMES = ['Dockerfile', 'Makefile', 'Caddyfile', '.gitignore'];

/** Lower-cased extension (".tar.gz" counts as one). */
export function ext(name: string): string {
	const lower = name.toLowerCase();
	if (lower.endsWith('.tar.gz') && lower.length > 7) return 'tar.gz';
	const i = lower.lastIndexOf('.');
	return i <= 0 ? '' : lower.slice(i + 1);
}

export function kindOf(name: string): Kind {
	const base = name.slice(name.lastIndexOf('/') + 1);
	if (TEXT_FILENAMES.includes(base) || base === '.env' || base.startsWith('.env.')) return 'text';
	return table[ext(base)] ?? 'other';
}

/** Kinds whose browser preview uses the native element directly. */
export const NATIVE_IMAGE = new Set(['jpg', 'jpeg', 'jfif', 'png', 'gif', 'webp', 'avif', 'bmp', 'ico', 'svg']);
