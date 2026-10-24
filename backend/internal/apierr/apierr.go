// Package apierr defines the error type services return for client-visible
// failures. The HTTP layer renders it with the standard error envelope.
package apierr

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"syscall"
)

// Error is a client-visible error with an HTTP status and a stable code.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	// RetryAfter, when > 0, is sent as a Retry-After header (seconds).
	RetryAfter int
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an error.
func New(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// With returns a copy with a detail added.
func (e *Error) With(k string, v any) *Error {
	c := *e
	c.Details = map[string]any{}
	for dk, dv := range e.Details {
		c.Details[dk] = dv
	}
	c.Details[k] = v
	return &c
}

// As extracts an *Error from err.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Is reports whether err is an *Error with the given code.
func Is(err error, code string) bool {
	e, ok := As(err)
	return ok && e.Code == code
}

// Common errors.
var (
	ErrInvalidPath       = New(http.StatusBadRequest, "invalid_path", "Invalid path")
	ErrReservedName      = New(http.StatusForbidden, "reserved_name", "This name is reserved by Filet")
	ErrPlaceNotFound     = New(http.StatusNotFound, "place_not_found", "Unknown place")
	ErrNotFound          = New(http.StatusNotFound, "not_found", "Not found")
	ErrNotADirectory     = New(http.StatusBadRequest, "not_a_directory", "Not a directory")
	ErrIsADirectory      = New(http.StatusBadRequest, "is_a_directory", "Is a directory")
	ErrPermissionDenied  = New(http.StatusForbidden, "permission_denied", "Permission denied")
	ErrPlaceReadOnly     = New(http.StatusForbidden, "place_readonly", "This place is read-only")
	ErrAlreadyExists     = New(http.StatusConflict, "already_exists", "A file or folder with that name already exists")
	ErrParentNotDir      = New(http.StatusConflict, "parent_not_directory", "A path component is not a directory")
	ErrUploadInProgress  = New(http.StatusConflict, "upload_in_progress", "An upload is in progress at this path")
	ErrInvalidDest       = New(http.StatusBadRequest, "invalid_destination", "Cannot copy or move a folder into itself")
	ErrInsufficient      = New(http.StatusInsufficientStorage, "insufficient_storage", "Not enough free space")
	ErrTrashUnavailable  = New(http.StatusServiceUnavailable, "trash_unavailable", "Trash is not available for this place")
	ErrInvalidRequest    = New(http.StatusBadRequest, "invalid_request", "Invalid request")
	ErrUnauthenticated   = New(http.StatusUnauthorized, "unauthenticated", "Not signed in")
	ErrInternal          = New(http.StatusInternalServerError, "internal", "Internal error")
	ErrNoThumbnail       = New(http.StatusNotFound, "no_thumbnail", "No thumbnail")
	ErrNotMedia          = New(http.StatusBadRequest, "not_media", "Not a media file")
	ErrUnsupportedFormat = New(http.StatusBadRequest, "unsupported_format", "Unsupported format")
	ErrTooLarge          = New(http.StatusRequestEntityTooLarge, "too_large", "Too large")
	ErrBusy              = New(http.StatusServiceUnavailable, "busy", "Server busy, retry shortly")
)

// Invalid returns invalid_request naming a field.
func Invalid(field, msg string) *Error {
	return New(http.StatusBadRequest, "invalid_request", msg).With("field", field)
}

// ToolUnavailable is returned when an external tool is missing.
func ToolUnavailable(tool string) *Error {
	return New(http.StatusNotImplemented, "tool_unavailable", tool+" is not installed").With("tool", tool)
}

// IsEscape reports whether err is os.Root's "path escapes from parent".
func IsEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

// FromFS maps a filesystem error to a client error. Unknown errors are
// returned unchanged (rendered as 500).
func FromFS(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := As(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, fs.ErrNotExist), IsEscape(err):
		return ErrNotFound
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return ErrPermissionDenied
	case errors.Is(err, syscall.ENOTDIR):
		return ErrParentNotDir
	case errors.Is(err, fs.ErrExist), errors.Is(err, syscall.EEXIST), errors.Is(err, syscall.ENOTEMPTY):
		return ErrAlreadyExists
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return ErrInsufficient
	case errors.Is(err, syscall.ENAMETOOLONG):
		return ErrInvalidPath
	case errors.Is(err, syscall.ELOOP):
		return ErrNotFound
	}
	return err
}
