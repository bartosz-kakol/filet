import type { ArchiveEntry } from '$lib/api/types';
import { compareNames } from '$lib/files/sort';

export interface TreeNode {
	name: string;
	path: string;
	isDir: boolean;
	entry?: ArchiveEntry;
	children: TreeNode[];
}

/** Builds a tree, synthesising implied parent directories. */
export function buildTree(entries: ArchiveEntry[]): TreeNode {
	const root: TreeNode = { name: '', path: '', isDir: true, children: [] };
	const dirs = new Map<string, TreeNode>([['', root]]);
	const dirFor = (path: string): TreeNode => {
		let d = dirs.get(path);
		if (d) return d;
		const i = path.lastIndexOf('/');
		const parent = dirFor(i < 0 ? '' : path.slice(0, i));
		d = { name: path.slice(i + 1), path, isDir: true, children: [] };
		parent.children.push(d);
		dirs.set(path, d);
		return d;
	};
	for (const e of entries) {
		if (e.isDir) {
			const d = dirFor(e.path);
			d.entry = e;
			continue;
		}
		const i = e.path.lastIndexOf('/');
		const parent = dirFor(i < 0 ? '' : e.path.slice(0, i));
		parent.children.push({ name: e.path.slice(i + 1), path: e.path, isDir: false, entry: e, children: [] });
	}
	const sort = (n: TreeNode) => {
		n.children.sort((a, b) => (a.isDir !== b.isDir ? (a.isDir ? -1 : 1) : compareNames(a.name, b.name)));
		n.children.forEach(sort);
	};
	sort(root);
	return root;
}

export interface FlatRow {
	node: TreeNode;
	depth: number;
}

/** Flattens the expanded part of the tree for a virtual list. */
export function flatten(root: TreeNode, expanded: Set<string>): FlatRow[] {
	const out: FlatRow[] = [];
	const walk = (n: TreeNode, depth: number) => {
		for (const c of n.children) {
			out.push({ node: c, depth });
			if (c.isDir && expanded.has(c.path)) walk(c, depth + 1);
		}
	};
	walk(root, 0);
	return out;
}
