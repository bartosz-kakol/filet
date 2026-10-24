package uploads

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"

	"filet/internal/apierr"
	"filet/internal/db/gen"
	"filet/internal/fsops"
	"filet/internal/places"
)

// PreflightItem is one file of a batch.
type PreflightItem struct {
	Key   string `json:"key"`
	Place string `json:"place"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
}

// PreflightResult is the verdict for one item.
type PreflightResult struct {
	Key              string        `json:"key"`
	Status           string        `json:"status"`
	Existing         *places.Entry `json:"existing,omitempty"`
	SuggestedName    string        `json:"suggestedName,omitempty"`
	Required         int64         `json:"required,omitempty"`
	Available        uint64        `json:"available,omitempty"`
	ReceivedBytes    int64         `json:"receivedBytes,omitempty"`
	ReplacesLeftover bool          `json:"replacesLeftover,omitempty"`
}

type fsState struct {
	free uint64
	used uint64
}

type placeSessions struct {
	byTarget map[string]gen.UploadSession
	byDir    map[string][]gen.UploadSession
}

// Preflight checks a batch before any hashing: read-only, validation,
// parents, permissions, conflicts, resumable leftovers and cumulative free
// space per filesystem. It reserves nothing.
func (s *Service) Preflight(ctx context.Context, items []PreflightItem) ([]PreflightResult, error) {
	out := make([]PreflightResult, len(items))
	sessions := map[string]*placeSessions{}
	statfsByDir := map[string]string{} // place|dir → fsid
	fsStates := map[string]*fsState{}
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r := &out[i]
		r.Key = it.Key
		p, err := s.reg.Get(it.Place)
		if err != nil {
			r.Status = "place_not_found"
			continue
		}
		if p.ReadOnly {
			r.Status = "readonly"
			continue
		}
		target, err := places.ValidateRelPath(it.Path)
		if err != nil || target == "/" || it.Size < 0 {
			r.Status = "invalid_path"
			if apierr.Is(err, "reserved_name") {
				r.Status = "reserved_name"
			}
			continue
		}
		dir, name := places.Split(target)
		nearest, st := nearestDir(p, dir)
		if st != "" {
			r.Status = st
			continue
		}
		ps, err := s.placeSessions(ctx, p.ID, sessions)
		if err != nil {
			return nil, err
		}
		need := it.Size
		existingSession, hasSession := ps.byTarget[target]
		if hasSession && isActive(existingSession) {
			r.Status = "uploading"
			r.SuggestedName = suggestPreflight(p, ps, dir, name, it.Size)
		} else if lfi, err := p.Root.Lstat(places.RootRel(target)); err == nil {
			e := p.FullView().BuildEntry(target, name, lfi, nil)
			r.Status = "conflict"
			r.Existing = &e
			r.SuggestedName = suggestPreflight(p, ps, dir, name, it.Size)
		} else if !errors.Is(err, fs.ErrNotExist) {
			r.Status = "permission_denied"
			continue
		} else if hasSession && existingSession.Size == it.Size && it.Size > 0 {
			r.Status = "resumable"
			r.ReceivedBytes = receivedBytes(existingSession)
			need = 0
		} else {
			r.Status = "ok"
			r.ReplacesLeftover = hasSession
		}

		// Cumulative free-space check per filesystem.
		key := p.ID + "\x00" + nearest
		fsid, ok := statfsByDir[key]
		if !ok {
			df, err := fsops.OpenDir(p.Root, nearest)
			if err != nil {
				continue
			}
			du, err := s.Statfs(df)
			_ = df.Close()
			if err != nil {
				s.log.Debug("preflight statfs failed", "err", err)
				continue
			}
			fsid = du.Fsid
			statfsByDir[key] = fsid
			if _, ok := fsStates[fsid]; !ok {
				fsStates[fsid] = &fsState{free: du.Free}
			}
		}
		fst := fsStates[fsid]
		if need <= 0 {
			continue
		}
		if fst.used+uint64(need) > fst.free {
			if r.Status == "ok" {
				r.Status = "insufficient_space"
				r.Required = it.Size
				r.Available = fst.free - min(fst.used, fst.free)
				r.ReplacesLeftover = false
			}
			continue
		}
		fst.used += uint64(need)
	}
	return out, nil
}

// nearestDir walks dir's ancestors from the top and returns the deepest
// existing directory, or a failure status.
func nearestDir(p *places.Place, dir string) (string, string) {
	nearest := "/"
	if dir != "/" {
		cur := ""
		for _, seg := range splitSegs(dir) {
			cur += "/" + seg
			fi, err := p.Root.Stat(places.RootRel(cur))
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			if err != nil {
				return "", "permission_denied"
			}
			if !fi.IsDir() {
				return "", "parent_not_directory"
			}
			nearest = cur
		}
	}
	fi, err := p.Root.Stat(places.RootRel(nearest))
	if err != nil || !p.CanWriteDir(fi) {
		return "", "permission_denied"
	}
	return nearest, ""
}

func splitSegs(p string) []string {
	var out []string
	start := 1
	for i := 1; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return out
}

func (s *Service) placeSessions(ctx context.Context, placeID string, cache map[string]*placeSessions) (*placeSessions, error) {
	if ps, ok := cache[placeID]; ok {
		return ps, nil
	}
	rows, err := s.db.R.ListUploadSessionsInPlace(ctx, placeID)
	if err != nil {
		return nil, err
	}
	ps := &placeSessions{byTarget: map[string]gen.UploadSession{}, byDir: map[string][]gen.UploadSession{}}
	for _, r := range rows {
		ps.byTarget[r.TargetPath] = r
		ps.byDir[r.Dir] = append(ps.byDir[r.Dir], r)
	}
	cache[placeID] = ps
	return ps, nil
}

// suggestPreflight applies the keep-both rule with one exception: a name
// held by a leftover session of the same size counts as free, so "keep
// both" resumes an earlier keep-both upload.
func suggestPreflight(p *places.Place, ps *placeSessions, dir, name string, size int64) string {
	return places.KeepBothName(name, false, func(n string) bool {
		for _, r := range ps.byDir[dir] {
			if r.Name == n && (isActive(r) || r.Size != size) {
				return true
			}
		}
		_, err := p.Root.Lstat(places.RootRel(places.Join(dir, n)))
		return err == nil
	})
}

// Reconcile repairs upload state at startup.
func (s *Service) Reconcile(ctx context.Context, log *slog.Logger) error {
	rows, err := s.db.W.ListUploadSessions(ctx)
	if err != nil {
		return err
	}
	var kept, completed, dropped int
	for _, r := range rows {
		p, err := s.reg.Get(r.PlaceID)
		if err != nil {
			kept++ // placeMissing leftover; removable from the Leftovers view
			continue
		}
		partialRel := places.RootRel(places.Join(r.Dir, r.PartialName))
		if fi, err := p.Root.Lstat(partialRel); err == nil && fi.Mode().IsRegular() && fi.Size() == r.Size {
			kept++
			continue
		} else if err == nil {
			_ = p.Root.Remove(partialRel)
		} else if fi, err := p.Root.Lstat(places.RootRel(r.TargetPath)); err == nil && fi.Mode().IsRegular() &&
			fi.Size() == r.Size && (!r.ClientMtime.Valid || fi.ModTime().UnixMilli() == r.ClientMtime.Int64) {
			completed++
			if _, err := s.db.W.DeleteUploadSession(ctx, r.ID); err != nil {
				return err
			}
			continue
		}
		dropped++
		if _, err := s.db.W.DeleteUploadSession(ctx, r.ID); err != nil {
			return err
		}
	}
	log.Info("reconciled uploads", "kept", kept, "completed", completed, "dropped", dropped)
	return nil
}
