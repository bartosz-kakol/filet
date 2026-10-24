/**
 * Text sniff: the bytes decode as UTF-8 (a multi-byte
 * sequence split at the end is tolerated) and contain no NUL.
 */
export function looksLikeText(bytes: Uint8Array): boolean {
	if (bytes.includes(0)) return false;
	for (let cut = 0; cut <= 3 && cut <= bytes.length; cut++) {
		try {
			new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(0, bytes.length - cut));
			return true;
		} catch {
			/* try trimming a split sequence */
		}
	}
	return false;
}

export const SMALL_TEXT_LIMIT = 2 * 1024 * 1024;
