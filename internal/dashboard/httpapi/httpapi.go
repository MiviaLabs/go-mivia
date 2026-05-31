package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/*
var assets embed.FS

func RegisterRoutes(mux *http.ServeMux) {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	})
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/", http.StatusFound)
	})
	mux.Handle("GET /dashboard/", http.StripPrefix("/dashboard/", securityHeaders(files)))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self'",
			"connect-src 'self'",
			"img-src 'self'",
			"base-uri 'none'",
			"form-action 'none'",
			"frame-ancestors 'none'",
		}, "; "))
		next.ServeHTTP(w, r)
	})
}
