package places

import (
	"path"
	"strings"

	"filet/internal/tools"
)

// File kinds.
const (
	KindImage       = "image"
	KindImageRender = "image-render"
	KindVideo       = "video"
	KindAudio       = "audio"
	KindMarkdown    = "markdown"
	KindText        = "text"
	KindCSV         = "csv"
	KindPDF         = "pdf"
	KindArchive     = "archive"
	KindFont        = "font"
	KindOther       = "other"
	KindDir         = "dir"
)

var rawExts = []string{"dng", "cr2", "cr3", "nef", "arw", "raf", "orf", "rw2", "pef", "srw"}

// kindTable maps lower-cased extensions to kinds. It is mirrored in
// frontend/src/lib/files/kinds.ts; kinds_test.go and the Vitest suite both
// check testdata/kinds.json so the two stay in sync.
var kindTable = func() map[string]string {
	m := map[string]string{}
	add := func(kind string, exts ...string) {
		for _, e := range exts {
			m[e] = kind
		}
	}
	add(KindImage, "jpg", "jpeg", "jfif", "png", "gif", "webp", "avif", "bmp", "ico", "svg")
	add(KindImageRender, "heic", "heif", "tif", "tiff")
	add(KindImageRender, rawExts...)
	add(KindVideo, "mp4", "m4v", "mov", "webm", "mkv", "avi", "wmv", "flv", "ts", "m2ts", "mts", "mpg", "mpeg", "3gp", "ogv")
	add(KindAudio, "mp3", "m4a", "aac", "flac", "wav", "ogg", "oga", "opus", "wma", "aiff", "aif", "ape", "wv", "mka")
	add(KindMarkdown, "md", "markdown", "mdown")
	add(KindText, "txt", "log", "json", "jsonc", "yaml", "yml", "toml", "ini", "conf", "cfg", "env", "properties",
		"xml", "html", "htm", "css", "scss", "less", "js", "mjs", "cjs", "ts", "tsx", "jsx", "svelte", "vue", "go", "rs",
		"py", "rb", "php", "java", "kt", "kts", "c", "h", "cpp", "cc", "hpp", "cs", "swift", "sh", "bash", "zsh", "fish",
		"ps1", "bat", "sql", "lua", "pl", "r", "dart", "nix", "tf", "hcl", "gradle", "proto", "graphql", "diff", "patch",
		"srt", "vtt")
	add(KindCSV, "csv", "tsv")
	add(KindPDF, "pdf")
	add(KindArchive, "zip", "tar", "tar.gz", "tgz", "7z")
	add(KindFont, "ttf", "otf", "woff", "woff2")
	// "ts" is ambiguous between video and text (TypeScript); MPEG-TS wins
	// because .ts files in a media library are far more common than TypeScript
	// in one, and the text sniff still lets TypeScript open as text.
	m["ts"] = KindVideo
	return m
}()

var textFilenames = map[string]bool{"Dockerfile": true, "Makefile": true, "Caddyfile": true, ".gitignore": true}

var mimeTable = map[string]string{
	"jpg": "image/jpeg", "jpeg": "image/jpeg", "jfif": "image/jpeg", "png": "image/png", "gif": "image/gif",
	"webp": "image/webp", "avif": "image/avif", "bmp": "image/bmp", "ico": "image/x-icon", "svg": "image/svg+xml",
	"heic": "image/heic", "heif": "image/heif", "tif": "image/tiff", "tiff": "image/tiff",
	"dng": "image/x-adobe-dng", "cr2": "image/x-canon-cr2", "cr3": "image/x-canon-cr3", "nef": "image/x-nikon-nef",
	"arw": "image/x-sony-arw", "raf": "image/x-fuji-raf", "orf": "image/x-olympus-orf", "rw2": "image/x-panasonic-rw2",
	"pef": "image/x-pentax-pef", "srw": "image/x-samsung-srw",
	"mp4": "video/mp4", "m4v": "video/mp4", "mov": "video/quicktime", "webm": "video/webm", "mkv": "video/x-matroska",
	"avi": "video/x-msvideo", "wmv": "video/x-ms-wmv", "flv": "video/x-flv", "ts": "video/mp2t", "m2ts": "video/mp2t",
	"mts": "video/mp2t", "mpg": "video/mpeg", "mpeg": "video/mpeg", "3gp": "video/3gpp", "ogv": "video/ogg",
	"mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac", "flac": "audio/flac", "wav": "audio/wav",
	"ogg": "audio/ogg", "oga": "audio/ogg", "opus": "audio/ogg", "wma": "audio/x-ms-wma", "aiff": "audio/aiff",
	"aif": "audio/aiff", "ape": "audio/x-ape", "wv": "audio/x-wavpack", "mka": "audio/x-matroska",
	"md": "text/markdown", "markdown": "text/markdown", "mdown": "text/markdown",
	"txt": "text/plain", "log": "text/plain", "json": "application/json", "jsonc": "application/json",
	"yaml": "application/yaml", "yml": "application/yaml", "toml": "application/toml", "ini": "text/plain",
	"conf": "text/plain", "cfg": "text/plain", "env": "text/plain", "properties": "text/plain",
	"xml": "application/xml", "html": "text/html", "htm": "text/html", "css": "text/css", "scss": "text/x-scss",
	"less": "text/x-less", "js": "text/javascript", "mjs": "text/javascript", "cjs": "text/javascript",
	"tsx": "text/plain", "jsx": "text/javascript", "svelte": "text/plain", "vue": "text/plain", "go": "text/x-go",
	"rs": "text/x-rust", "py": "text/x-python", "rb": "text/x-ruby", "php": "text/x-php", "java": "text/x-java",
	"kt": "text/x-kotlin", "kts": "text/x-kotlin", "c": "text/x-c", "h": "text/x-c", "cpp": "text/x-c++",
	"cc": "text/x-c++", "hpp": "text/x-c++", "cs": "text/x-csharp", "swift": "text/x-swift",
	"sh": "application/x-sh", "bash": "application/x-sh", "zsh": "application/x-sh", "fish": "text/plain",
	"ps1": "text/plain", "bat": "text/plain", "sql": "application/sql", "lua": "text/x-lua", "pl": "text/x-perl",
	"r": "text/plain", "dart": "text/plain", "nix": "text/plain", "tf": "text/plain", "hcl": "text/plain",
	"gradle": "text/plain", "proto": "text/plain", "graphql": "application/graphql", "diff": "text/x-diff",
	"patch": "text/x-diff", "srt": "application/x-subrip", "vtt": "text/vtt",
	"csv": "text/csv", "tsv": "text/tab-separated-values", "pdf": "application/pdf",
	"zip": "application/zip", "tar": "application/x-tar", "tar.gz": "application/gzip", "tgz": "application/gzip",
	"7z":  "application/x-7z-compressed",
	"ttf": "font/ttf", "otf": "font/otf", "woff": "font/woff", "woff2": "font/woff2",
}

// Ext returns the lower-cased extension used for classification: the last
// two segments for .tar.gz, otherwise the last one. Names whose only dot is
// leading have no extension.
func Ext(name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".tar.gz") && len(lower) > len(".tar.gz") {
		return "tar.gz"
	}
	i := strings.LastIndexByte(lower, '.')
	if i <= 0 {
		return ""
	}
	return lower[i+1:]
}

// KindOf classifies a file name.
func KindOf(name string) string {
	base := path.Base(name)
	if textFilenames[base] || base == ".env" || strings.HasPrefix(base, ".env.") {
		return KindText
	}
	if k, ok := kindTable[Ext(base)]; ok {
		return k
	}
	return KindOther
}

// MimeOf returns the MIME type for a file name (never sniffed).
func MimeOf(name string) string {
	if m, ok := mimeTable[Ext(path.Base(name))]; ok {
		return m
	}
	if KindOf(name) == KindText {
		return "text/plain"
	}
	return "application/octet-stream"
}

// IsRaw reports whether name is a camera RAW file.
func IsRaw(name string) bool {
	e := Ext(name)
	for _, r := range rawExts {
		if e == r {
			return true
		}
	}
	return false
}

// EffectiveKind adjusts KindOf for tool capabilities: RAW files are only
// image-render when a RAW-capable vips loader exists.
func EffectiveKind(name string, caps *tools.Caps) string {
	k := KindOf(name)
	if k == KindImageRender && IsRaw(name) && (caps == nil || !caps.RAW) {
		return KindOther
	}
	return k
}

// Thumbable reports whether a thumbnail can be generated for name.
func Thumbable(name string, caps *tools.Caps) bool {
	if caps == nil {
		return false
	}
	switch EffectiveKind(name, caps) {
	case KindImage:
		if Ext(name) == "svg" {
			return caps.VipsThumbnail && caps.SVG
		}
		if Ext(name) == "ico" {
			return caps.VipsThumbnail && caps.Magick
		}
		return caps.VipsThumbnail
	case KindImageRender:
		switch Ext(name) {
		case "heic", "heif":
			return caps.VipsThumbnail && caps.HEIF
		case "tif", "tiff":
			return caps.VipsThumbnail
		}
		return caps.VipsThumbnail && caps.RAW
	case KindVideo:
		return caps.FFmpeg && caps.FFprobe && caps.FFmpegWebP
	case KindAudio:
		return caps.FFmpeg && caps.FFmpegWebP
	case KindPDF:
		return caps.VipsThumbnail && caps.PDF
	}
	return false
}

// KindFixture is the shape of testdata/kinds.json.
type KindFixture struct {
	Extensions map[string]string `json:"extensions"`
	Filenames  map[string]string `json:"filenames"`
	Mimes      map[string]string `json:"mimes"`
}

// Fixture exports the tables for the parity test.
func Fixture() KindFixture {
	f := KindFixture{Extensions: map[string]string{}, Filenames: map[string]string{}, Mimes: map[string]string{}}
	for k, v := range kindTable {
		f.Extensions[k] = v
	}
	for k := range textFilenames {
		f.Filenames[k] = KindText
	}
	f.Filenames[".env"] = KindText
	f.Filenames[".env.local"] = KindText
	for k, v := range mimeTable {
		f.Mimes[k] = v
	}
	return f
}
