// Package places holds the configured places, confined file access through
// os.Root, path validation, directory listing and entry objects.
package places

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"filet/internal/apierr"
	"filet/internal/config"
	"filet/internal/fsx"
	"filet/internal/tools"
)

// FiletDir is the private per-place directory name.
const FiletDir = ".filet"

// Place is one configured root directory.
type Place struct {
	ID           string
	Name         string
	Path         string // resolved absolute path
	ReadOnly     bool
	Root         *os.Root
	ThumbDir     string // absolute; <Path>/.filet/thumbs or the fallback
	HasTrash     bool   // .filet/trash usable
	SmartDevices []string

	reg *Registry
}

// TrashRel is the root-relative trash directory.
const TrashRel = FiletDir + "/trash"

// Registry is the set of places.
type Registry struct {
	list []*Place
	byID map[string]*Place
	Caps *tools.Caps
	ids  procIdentity
}

// NewRegistry opens a root per place and prepares .filet/. cacheBase is the
// fallback thumbnail base (<dir(database)>/cache).
func NewRegistry(cfg []config.PlaceConfig, cacheBase string, caps *tools.Caps, log *slog.Logger) (*Registry, error) {
	r := &Registry{byID: map[string]*Place{}, Caps: caps, ids: currentIdentity()}
	for _, pc := range cfg {
		root, err := os.OpenRoot(pc.ResolvedPath)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("place %q: %w", pc.Name, err)
		}
		p := &Place{
			ID: pc.ID, Name: pc.Name, Path: pc.ResolvedPath, ReadOnly: pc.ReadOnly, Root: root,
			SmartDevices: pc.SmartDevices, reg: r,
		}
		p.prepare(cacheBase, log)
		r.list = append(r.list, p)
		r.byID[p.ID] = p
	}
	return r, nil
}

func (p *Place) prepare(cacheBase string, log *slog.Logger) {
	thumbsOK := false
	if err := p.Root.MkdirAll(FiletDir+"/thumbs", 0o700); err == nil {
		if f, err := p.Root.OpenFile(FiletDir+"/thumbs/.probe", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
			_ = f.Close()
			_ = p.Root.Remove(FiletDir + "/thumbs/.probe")
			thumbsOK = true
		}
	}
	if thumbsOK {
		p.ThumbDir = filepath.Join(p.Path, FiletDir, "thumbs")
	} else {
		p.ThumbDir = filepath.Join(cacheBase, p.ID, "thumbs")
		if err := os.MkdirAll(p.ThumbDir, 0o700); err != nil {
			log.Error("cannot create fallback thumbnail directory", "place", p.Name, "dir", p.ThumbDir, "err", err)
		}
		log.Warn("place .filet/ not writable; using fallback thumbnail cache", "place", p.Name, "dir", p.ThumbDir)
	}
	if p.ReadOnly {
		return
	}
	if err := p.Root.MkdirAll(TrashRel, 0o700); err != nil {
		log.Error("cannot create trash directory; deleting is unavailable", "place", p.Name, "err", err)
		return
	}
	p.HasTrash = true
}

// Close closes all roots.
func (r *Registry) Close() {
	for _, p := range r.list {
		_ = p.Root.Close()
	}
}

// Get returns a place by id.
func (r *Registry) Get(id string) (*Place, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, apierr.ErrPlaceNotFound
	}
	return p, nil
}

// All returns places in config order.
func (r *Registry) All() []*Place { return r.list }

// Writable returns place_readonly for read-only places.
func (p *Place) Writable() error {
	if p.ReadOnly {
		return apierr.ErrPlaceReadOnly
	}
	return nil
}

// DiskUsage returns statfs data for the place.
func (p *Place) DiskUsage() (fsx.DiskUsage, error) { return fsx.Statfs(p.Path) }

// IsReserved reports whether a single name is reserved.
func IsReserved(name string) bool {
	return name == FiletDir || strings.HasPrefix(name, ".filet-part-") || strings.HasPrefix(name, ".filet-copy-")
}

// ValidateRelPath validates a client-supplied place path ("/a/b"). Paths are
// never cleaned: a non-canonical path is an error.
func ValidateRelPath(p string) (string, error) {
	if len(p) == 0 || len(p) > 4096 || p[0] != '/' || !utf8.ValidString(p) || strings.IndexByte(p, 0) >= 0 {
		return "", apierr.ErrInvalidPath
	}
	if p == "/" {
		return p, nil
	}
	reserved := false
	for seg := range strings.SplitSeq(p[1:], "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > 255 {
			return "", apierr.ErrInvalidPath
		}
		if IsReserved(seg) {
			reserved = true
		}
	}
	if reserved {
		return "", apierr.ErrReservedName
	}
	return p, nil
}

// ValidateName validates a new single-segment name.
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\x00") || !utf8.ValidString(name) {
		return apierr.New(400, "invalid_path", "Invalid name")
	}
	if IsReserved(name) {
		return apierr.ErrReservedName
	}
	return nil
}

// RootRel converts "/a/b" to the os.Root form "a/b" ("." for the root).
func RootRel(p string) string {
	if p == "/" || p == "" {
		return "."
	}
	return strings.TrimPrefix(p, "/")
}

// Join joins a place directory and a name ("/" + "x" → "/x").
func Join(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

// Split splits "/a/b" into "/a" and "b". The root splits into "/", "".
func Split(p string) (dir, name string) {
	if p == "/" {
		return "/", ""
	}
	i := strings.LastIndexByte(p, '/')
	if i == 0 {
		return "/", p[1:]
	}
	return p[:i], p[i+1:]
}

// IsWithin reports whether p equals dir or lies below it.
func IsWithin(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// KeepBothName returns the first free "base (n).ext" name. ext is the part
// from the last dot, except for directories and names whose only dot is
// leading.
func KeepBothName(name string, isDir bool, taken func(string) bool) string {
	base, ext := name, ""
	if !isDir {
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			base, ext = name[:i], name[i:]
		}
	}
	for n := 1; ; n++ {
		cand := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if len(cand) > 255 {
			cut := len(base) - (len(cand) - 255)
			if cut <= 0 {
				return cand[:255]
			}
			cand = fmt.Sprintf("%s (%d)%s", truncateUTF8(base, cut), n, ext)
		}
		if !taken(cand) {
			return cand
		}
	}
}

func truncateUTF8(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// RealPath opens rel through the view's root and returns the absolute host
// path the kernel resolved, checked to lie inside the place. It is used to
// hand paths to external tools, which cannot be confined by os.Root.
func (v *View) RealPath(rel string) (string, error) {
	f, err := v.Root.Open(RootRel(rel))
	if err != nil {
		return "", apierr.FromFS(err)
	}
	defer f.Close()
	rp, err := fsx.RealPath(f)
	if err != nil {
		return "", err
	}
	// Place.Path was resolved with EvalSymlinks at startup.
	if base := v.Place.Path; rp != base && !strings.HasPrefix(rp, base+"/") {
		return "", apierr.ErrNotFound
	}
	return rp, nil
}

// View is a confined window onto a place: the whole place for authenticated
// requests, or the shared folder/file for public share requests.
type View struct {
	Place *Place
	Root  *os.Root
	// Base is the place-relative path of Root ("/" for the whole place).
	Base string
	// Public hides dotfiles and disables stream links.
	Public bool
	// OnlyName, when set, restricts the view to the single file "/OnlyName".
	OnlyName string
}

// FullView returns the whole-place view.
func (p *Place) FullView() *View { return &View{Place: p, Root: p.Root, Base: "/"} }

// PlacePath converts a view path to a place path.
func (v *View) PlacePath(rel string) string {
	if v.Base == "/" {
		return rel
	}
	if rel == "/" {
		return v.Base
	}
	return v.Base + rel
}

// Check validates rel and applies the view restrictions.
func (v *View) Check(rel string) (string, error) {
	rel, err := ValidateRelPath(rel)
	if err != nil {
		return "", err
	}
	if v.OnlyName != "" && rel != "/"+v.OnlyName {
		return "", apierr.ErrNotFound
	}
	if v.Public && hasDotSegment(rel) {
		return "", apierr.ErrNotFound
	}
	return rel, nil
}

func hasDotSegment(p string) bool {
	for seg := range strings.SplitSeq(strings.TrimPrefix(p, "/"), "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// Stat follows symlinks (confined).
func (v *View) Stat(rel string) (fs.FileInfo, error) { return v.Root.Stat(RootRel(rel)) }

// Lstat does not follow the final symlink.
func (v *View) Lstat(rel string) (fs.FileInfo, error) { return v.Root.Lstat(RootRel(rel)) }

// Open opens a file (confined).
func (v *View) Open(rel string) (*os.File, error) { return v.Root.Open(RootRel(rel)) }

// OpenRegular opens rel and checks that it is a regular file.
func (v *View) OpenRegular(rel string) (*os.File, fs.FileInfo, error) {
	f, err := v.Open(rel)
	if err != nil {
		return nil, nil, apierr.FromFS(err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, apierr.FromFS(err)
	}
	if fi.IsDir() {
		_ = f.Close()
		return nil, nil, apierr.ErrIsADirectory
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, apierr.ErrNotFound
	}
	return f, fi, nil
}

// Entry is the JSON entry object.
type Entry struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Size      int64    `json:"size"`
	Mtime     int64    `json:"mtime"`
	Mode      string   `json:"mode"`
	Readable  bool     `json:"readable"`
	Hidden    bool     `json:"hidden"`
	Kind      string   `json:"kind"`
	Mime      string   `json:"mime"`
	Thumbable bool     `json:"thumbable"`
	Symlink   *Symlink `json:"symlink,omitempty"`
	Error     *string  `json:"error"`
	BadName   bool     `json:"badName"`

	// Ident is the file identity of the (followed) target; not serialised.
	Ident fsx.Identity `json:"-"`
}

// Symlink describes a symlink entry's resolution.
type Symlink struct {
	Status string `json:"status"` // ok | broken | outside
}

var permDenied = "permission_denied"

// IsOpenable reports whether the entry can be opened/copied/shared.
func (e *Entry) IsOpenable() bool {
	return e.Readable && e.Error == nil && !e.BadName && (e.Symlink == nil || e.Symlink.Status == "ok")
}

// BuildEntry builds an entry for name at rel inside the view, given its
// lstat info (nil if lstat failed with lerr).
func (v *View) BuildEntry(rel, name string, lfi fs.FileInfo, lerr error) Entry {
	display := name
	bad := false
	if !utf8.ValidString(name) {
		display = strings.ToValidUTF8(name, "�")
		bad = true
	}
	e := Entry{Name: display, BadName: bad, Hidden: strings.HasPrefix(name, ".")}
	if lfi == nil {
		e.Type = "unknown"
		e.Kind = KindOther
		e.Mime = "application/octet-stream"
		if lerr != nil && fsx.IsPermission(lerr) {
			e.Error = &permDenied
		}
		return e
	}
	fi := lfi
	if lfi.Mode()&fs.ModeSymlink != 0 {
		tfi, err := v.Stat(rel)
		switch {
		case err == nil:
			e.Symlink = &Symlink{Status: "ok"}
			fi = tfi
		case apierr.IsEscape(err):
			e.Symlink = &Symlink{Status: "outside"}
		default:
			e.Symlink = &Symlink{Status: "broken"}
		}
	}
	e.Mtime = fi.ModTime().UnixMilli()
	e.Mode = fmt.Sprintf("%04o", unixPerm(fi.Mode()))
	e.Ident = fsx.IdentityOf(fi)
	caps := v.Place.reg.Caps
	switch {
	case e.Symlink != nil && e.Symlink.Status != "ok":
		e.Type = "symlink"
		e.Kind = KindOther
		e.Mime = "application/octet-stream"
	case fi.IsDir():
		e.Type = "dir"
		e.Kind = KindDir
		e.Mime = "inode/directory"
		e.Readable = v.Place.reg.ids.canRead(fi, true)
	case fi.Mode().IsRegular():
		e.Type = "file"
		e.Size = fi.Size()
		e.Kind = EffectiveKind(name, caps)
		e.Mime = MimeOf(name)
		e.Readable = v.Place.reg.ids.canRead(fi, false)
		e.Thumbable = !bad && e.Readable && Thumbable(name, caps)
	default:
		e.Type = "other"
		e.Kind = KindOther
		e.Mime = "application/octet-stream"
	}
	return e
}

func unixPerm(m fs.FileMode) uint32 {
	p := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		p |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		p |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		p |= 0o1000
	}
	return p
}

// StatEntry returns the entry object for rel.
func (v *View) StatEntry(rel string) (Entry, error) {
	fi, err := v.Lstat(rel)
	if err != nil {
		return Entry{}, apierr.FromFS(err)
	}
	_, name := Split(v.PlacePath(rel))
	if rel == "/" {
		name = path.Base(v.PlacePath("/"))
		if name == "/" {
			name = v.Place.Name
		}
	}
	return v.BuildEntry(rel, name, fi, nil), nil
}

// Listing is the result of List (uploads are attached by the caller).
type Listing struct {
	Path        string  `json:"path"`
	DirWritable bool    `json:"dirWritable"`
	Entries     []Entry `json:"entries"`
}

// List lists a directory.
func (v *View) List(ctx context.Context, rel string) (*Listing, error) {
	f, err := v.Open(rel)
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, apierr.FromFS(err)
	}
	if !fi.IsDir() {
		return nil, apierr.ErrNotADirectory
	}
	out := &Listing{Path: rel, Entries: []Entry{}}
	out.DirWritable = !v.Place.ReadOnly && !v.Public && v.Place.reg.ids.canWrite(fi)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.ReadDir(1024)
		for _, de := range batch {
			name := de.Name()
			if IsReserved(name) {
				continue
			}
			if v.Public && strings.HasPrefix(name, ".") {
				continue
			}
			lfi, lerr := de.Info()
			if lerr != nil && errors.Is(lerr, fs.ErrNotExist) {
				continue // vanished
			}
			out.Entries = append(out.Entries, v.BuildEntry(Join(rel, name), name, lfi, lerr))
		}
		if err != nil {
			if errors.Is(err, os.ErrClosed) || len(batch) == 0 {
				break
			}
			if fsx.IsPermission(err) {
				return nil, apierr.ErrPermissionDenied
			}
			break
		}
	}
	slices.SortFunc(out.Entries, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// CanWriteDir reports whether the process may create entries in a directory.
func (r *Registry) CanWriteDir(fi fs.FileInfo) bool { return r.ids.canWrite(fi) }

// CanRead reports whether the process may read fi.
func (r *Registry) CanRead(fi fs.FileInfo) bool { return r.ids.canRead(fi, fi.IsDir()) }

// CanWriteDir reports whether the process may create entries in fi (a
// directory of this place).
func (p *Place) CanWriteDir(fi fs.FileInfo) bool { return p.reg.ids.canWrite(fi) }
