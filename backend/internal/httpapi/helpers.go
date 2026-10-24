package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"filet/internal/apierr"
	"filet/internal/places"
)

const (
	limitJSON      = 1 << 20
	limitRegister  = 32 << 20
	limitPreflight = 8 << 20
)

type errorBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeError renders err with the error envelope. Context cancellation
// (the client went away) writes nothing.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	ae, ok := apierr.As(apierr.FromFS(err))
	if !ok {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			ae = apierr.ErrTooLarge
		} else {
			log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			ae = apierr.ErrInternal
		}
	}
	var body errorBody
	body.Error.Code = ae.Code
	body.Error.Message = ae.Message
	body.Error.Details = ae.Details
	if ae.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(ae.RetryAfter))
	}
	writeJSON(w, ae.Status, body)
}

// decodeJSON reads a JSON body with a size limit.
func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return apierr.ErrTooLarge
		}
		if errors.Is(err, io.EOF) {
			return apierr.New(http.StatusBadRequest, "invalid_request", "Request body is required")
		}
		return apierr.New(http.StatusBadRequest, "invalid_request", "Malformed JSON: "+err.Error())
	}
	return nil
}

// pathParam turns the {path...} wildcard into a place path.
func pathParam(r *http.Request) string { return "/" + r.PathValue("path") }

// queryPath reads ?path= (default "/").
func queryPath(r *http.Request) string {
	if p := r.URL.Query().Get("path"); p != "" {
		return p
	}
	return "/"
}

// attrChar reports RFC 5987 attr-chars.
func attrChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

// contentDisposition builds an RFC 5987 inline/attachment header value.
func contentDisposition(kind, name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if attrChar(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return kind + "; filename*=UTF-8''" + b.String()
}

// safeContentType maps a file name to the Content-Type used for user
// content: never sniffed, active types downgraded to text/plain.
func safeContentType(name string) string {
	mime := places.MimeOf(name)
	switch mime {
	case "text/html", "application/xhtml+xml", "text/xml", "application/xml":
		return "text/plain; charset=utf-8"
	}
	if strings.HasPrefix(mime, "text/") && !strings.Contains(mime, "charset") {
		return mime + "; charset=utf-8"
	}
	return mime
}

// setUserFileHeaders applies the security headers for user-file responses.
func setUserFileHeaders(w http.ResponseWriter, name string, download bool) {
	h := w.Header()
	h.Set("Content-Type", safeContentType(name))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	if places.MimeOf(name) == "image/svg+xml" {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	}
	disp := "inline"
	if download {
		disp = "attachment"
	}
	h.Set("Content-Disposition", contentDisposition(disp, name))
}

func boolQuery(r *http.Request, key string) bool {
	v := r.URL.Query().Get(key)
	return v == "1" || v == "true"
}
