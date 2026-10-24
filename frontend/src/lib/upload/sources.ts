// Turns pickers and drops into a flat list of files plus every directory in
// the tree (including empty ones), relative to the drop target.

export interface PickedFile {
	file: File;
	/** Path relative to the target directory, e.g. "trip/clip.mp4". */
	rel: string;
}

export interface Picked {
	files: PickedFile[];
	/** Relative directories to create, parents first, e.g. ["trip", "trip/raw"]. */
	dirs: string[];
}

/** Grabs the entries synchronously; call inside the drop handler. */
export function entriesFromDrop(dt: DataTransfer): FileSystemEntry[] | null {
	if (!dt.items || !dt.items.length) return null;
	const out: FileSystemEntry[] = [];
	for (const item of Array.from(dt.items)) {
		if (item.kind !== 'file') continue;
		const e = item.webkitGetAsEntry?.();
		if (e) out.push(e);
	}
	return out.length ? out : null;
}

function readAll(reader: FileSystemDirectoryReader): Promise<FileSystemEntry[]> {
	return new Promise((resolve, reject) => {
		const all: FileSystemEntry[] = [];
		const next = () =>
			reader.readEntries((batch) => {
				if (!batch.length) resolve(all);
				else {
					all.push(...batch);
					next();
				}
			}, reject);
		next();
	});
}

function fileOf(e: FileSystemFileEntry): Promise<File> {
	return new Promise((resolve, reject) => e.file(resolve, reject));
}

/** Recursively reads dropped entries. */
export async function readEntries(entries: FileSystemEntry[]): Promise<Picked> {
	const files: PickedFile[] = [];
	const dirs: string[] = [];
	const walk = async (e: FileSystemEntry, prefix: string) => {
		const rel = prefix ? `${prefix}/${e.name}` : e.name;
		if (e.isFile) {
			files.push({ file: await fileOf(e as FileSystemFileEntry), rel });
		} else if (e.isDirectory) {
			dirs.push(rel);
			const children = await readAll((e as FileSystemDirectoryEntry).createReader());
			for (const c of children) await walk(c, rel);
		}
	};
	for (const e of entries) await walk(e, '');
	return { files, dirs };
}

/** Files from <input type=file> (webkitRelativePath set for folder pickers). */
export function fromFileList(list: FileList | File[]): Picked {
	const files: PickedFile[] = [];
	const dirs = new Set<string>();
	for (const f of Array.from(list)) {
		const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name;
		files.push({ file: f, rel });
		const parts = rel.split('/');
		for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/'));
	}
	return { files, dirs: [...dirs].sort((a, b) => a.split('/').length - b.split('/').length) };
}

/** Plain files from a drop without directory support. */
export function fromDataTransferFiles(dt: DataTransfer): Picked {
	return fromFileList(Array.from(dt.files));
}
