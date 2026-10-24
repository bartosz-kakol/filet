import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { KIND_TABLE, TEXT_FILENAMES, kindOf } from './kinds';

// The same fixture is checked by backend/internal/places/places_test.go.
const fixture = JSON.parse(readFileSync(new URL('../../../../backend/internal/places/testdata/kinds.json', import.meta.url), 'utf8')) as {
	extensions: Record<string, string>;
	filenames: Record<string, string>;
};

describe('kind table parity with the Go server', () => {
	it('extensions', () => {
		expect({ ...KIND_TABLE }).toEqual(fixture.extensions);
	});
	it('special filenames', () => {
		for (const [n, k] of Object.entries(fixture.filenames)) expect(kindOf(n)).toBe(k);
		for (const n of TEXT_FILENAMES) expect(fixture.filenames[n]).toBe('text');
	});
	it('double extensions', () => {
		expect(kindOf('backup.TAR.GZ')).toBe('archive');
		expect(kindOf('.bashrc')).toBe('other');
	});
});
