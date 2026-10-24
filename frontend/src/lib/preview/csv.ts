/** RFC 4180 parser: quoted fields, escaped quotes ("") and CRLF. */
export function parseCSV(text: string, delimiter = ',', maxRows = 100_000): { rows: string[][]; truncated: boolean } {
	const rows: string[][] = [];
	let row: string[] = [];
	let field = '';
	let i = 0;
	let quoted = false;
	const n = text.length;
	if (text.charCodeAt(0) === 0xfeff) i = 1;
	while (i < n) {
		const c = text[i];
		if (quoted) {
			if (c === '"') {
				if (text[i + 1] === '"') {
					field += '"';
					i += 2;
					continue;
				}
				quoted = false;
				i++;
				continue;
			}
			field += c;
			i++;
			continue;
		}
		if (c === '"' && field === '') {
			quoted = true;
			i++;
		} else if (c === delimiter) {
			row.push(field);
			field = '';
			i++;
		} else if (c === '\r' || c === '\n') {
			row.push(field);
			rows.push(row);
			row = [];
			field = '';
			i += c === '\r' && text[i + 1] === '\n' ? 2 : 1;
			if (rows.length >= maxRows) return { rows, truncated: i < n };
		} else {
			field += c;
			i++;
		}
	}
	if (field !== '' || row.length) {
		row.push(field);
		rows.push(row);
	}
	return { rows, truncated: false };
}
