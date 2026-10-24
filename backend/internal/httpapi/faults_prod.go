//go:build !filettest

package httpapi

import "net/http"

// faults is a no-op in production builds.
func faults(next http.Handler) http.Handler { return next }

func testRoutes(*http.ServeMux) {}
