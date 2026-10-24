// Shapes of the JSON the Go server returns.

export type EntryType = 'file' | 'dir' | 'symlink' | 'other' | 'unknown';

export type Kind =
	| 'image'
	| 'image-render'
	| 'video'
	| 'audio'
	| 'markdown'
	| 'text'
	| 'csv'
	| 'pdf'
	| 'archive'
	| 'font'
	| 'other'
	| 'dir';

export interface Entry {
	name: string;
	type: EntryType;
	size: number;
	mtime: number;
	mode: string;
	readable: boolean;
	hidden: boolean;
	kind: Kind;
	mime: string;
	thumbable: boolean;
	symlink?: { status: 'ok' | 'broken' | 'outside' };
	error: string | null;
	badName: boolean;
}

export interface UploadPlaceholder {
	id: string;
	name: string;
	size: number;
	receivedBytes: number;
	active: boolean;
}

export interface Listing {
	path: string;
	dirWritable: boolean;
	entries: Entry[];
	uploads: UploadPlaceholder[];
}

export interface SmartDevice {
	device: string;
	status: SmartStatus;
	model?: string;
	temperatureC: number | null;
	polledAt: number | null;
	asleepAt: number | null;
	reason?: string;
}

export type SmartStatus = 'ok' | 'warning' | 'failing' | 'standby' | 'unavailable';

export interface Place {
	id: string;
	name: string;
	readonly: boolean;
	trashAvailable: boolean;
	disk: { total: number; used: number; free: number; fsid: string } | null;
	smart: { status: SmartStatus; devices: SmartDevice[] } | null;
}

export interface Capabilities {
	thumbnails: {
		image: boolean;
		heic: boolean;
		raw: boolean;
		svg: boolean;
		pdf: boolean;
		video: boolean;
		audioCover: boolean;
	};
	renditions: boolean;
	mediaInfo: boolean;
	remux: boolean;
	smart: boolean;
}

export interface Settings {
	version: string;
	shareDefaultExpiryMs: number;
	streamLinkTtlMs: number;
	smartEnabled: boolean;
	capabilities: Capabilities;
}

export type JobStatus =
	| 'queued'
	| 'running'
	| 'awaiting_input'
	| 'completed'
	| 'failed'
	| 'cancelled'
	| 'interrupted';

export interface ConflictSide {
	path: string;
	entry: Entry;
}

export interface JobConflict {
	id: string;
	src: ConflictSide;
	dst: ConflictSide;
	sameItem: boolean;
	uploadInProgress: boolean;
	suggestedName: string;
}

export interface JobSpec {
	type: 'copy' | 'move' | 'trash' | 'restore' | 'purge';
	src?: { place: string; paths: string[] };
	dst?: { place: string; dir: string };
	place?: string;
	paths?: string[];
	ids?: string[];
	all?: boolean;
	conflict?: 'overwrite' | 'rename' | 'skip';
}

export interface Job {
	id: string;
	type: JobSpec['type'];
	status: JobStatus;
	spec: JobSpec;
	bytesTotal: number;
	bytesDone: number;
	itemsTotal: number;
	itemsDone: number;
	currentItem: string | null;
	pendingConflict: JobConflict | null;
	result: {
		skipped: number;
		renamed: { from: string; to: string }[];
		errors: { path: string; code: string }[];
	};
	error: { code: string; message: string } | null;
	createdAt: number;
	startedAt: number | null;
	finishedAt: number | null;
}

export interface TrashItem {
	id: string;
	name: string;
	originalPath: string;
	isDir: boolean;
	size: number | null;
	deletedAt: number;
	originUnknown: boolean;
}

export interface Leftover {
	id: string;
	place: string;
	placeName: string;
	path: string;
	size: number;
	chunkCount: number;
	receivedCount: number;
	receivedBytes: number;
	createdAt: number;
	lastActivityAt: number;
	placeMissing: boolean;
}

export interface ShareLink {
	id: string;
	url: string;
	place: string;
	placeName: string;
	placeMissing: boolean;
	path: string;
	isDir: boolean;
	expiresAt: number | null;
	hasPassword: boolean;
	createdAt: number;
	lastAccessedAt: number | null;
	accessCount: number;
	targetExists: boolean;
}

export interface PublicShareInfo {
	name: string;
	isDir: boolean;
	size: number;
	mtime: number;
	kind: Kind;
	mime?: string;
	thumbable?: boolean;
	expiresAt: number | null;
	passwordRequired: boolean;
	unlocked: boolean;
}

export interface MediaInfo {
	container: string;
	duration: number;
	bitrate: number;
	size: number;
	tags: Partial<Record<'title' | 'artist' | 'album' | 'albumArtist' | 'track' | 'date' | 'genre', string>>;
	video: {
		index: number;
		codec: string;
		profile?: string;
		level?: number;
		width: number;
		height: number;
		dar?: string;
		fps: number;
		pixFmt?: string;
		bitDepth?: number;
		hdr: string | null;
		rotation: number;
		bitrate?: number;
		default: boolean;
	}[];
	audio: {
		index: number;
		codec: string;
		profile?: string;
		channels: number;
		layout?: string;
		sampleRate: number;
		language: string | null;
		title: string | null;
		default: boolean;
	}[];
	subtitles: {
		index: number;
		codec: string;
		language: string | null;
		title: string | null;
		default: boolean;
		text: boolean;
	}[];
	chapters: { start: number; end: number; title: string | null }[];
	hasCoverArt: boolean;
	sidecarSubtitles: { name: string; language: string | null }[];
}

export interface TextIndex {
	size: number;
	mtime: number;
	lineCount: number;
	step: number;
	offsets: number[];
}

export interface ArchiveEntry {
	path: string;
	size: number;
	compressedSize?: number;
	mtime: number;
	isDir: boolean;
	encrypted: boolean;
}

export interface ArchiveListing {
	format: string;
	entryCount: number;
	totalSize: number;
	truncated: boolean;
	entries: ArchiveEntry[];
}

export interface SearchHit {
	path: string;
	entry: Entry;
}

export interface SearchDone {
	done: true;
	truncated: boolean;
	timedOut: boolean;
	scanned: number;
}
