// Package httpapi provides the public HTTP boundary for 0xbin.
package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/0atxl/0xbin/internal/config"
	"github.com/0atxl/0xbin/internal/ratelimit"
)

// ErrServerClosed is returned by Serve after a graceful shutdown.
var ErrServerClosed = http.ErrServerClosed

const (
	hostedDomain           = "0xbin.app"
	defaultPageTitle       = "0xbin — Ephemeral Paste Service"
	defaultPageDescription = "Create temporary text and code pastes with memorable links, automatic expiry, and optional client-side encryption."
)

type publicPageSEO struct {
	Path        string
	Title       string
	Description string
	Application bool
}

var homepageSEO = publicPageSEO{
	Path:        "/",
	Title:       defaultPageTitle,
	Description: defaultPageDescription,
	Application: true,
}

var hostedPublicPages = []publicPageSEO{
	{Path: "/about", Title: "Who & Why — 0xbin", Description: "Meet the independent maintainer behind 0xbin and learn why the project uses short expiry, memorable links, and optional browser-side encryption."},
	{Path: "/terms", Title: "Terms & Conditions — 0xbin", Description: "Terms, acceptable-use rules, and security guidance for the hosted 0xbin temporary paste service."},
	{Path: "/privacy", Title: "Privacy & Reports — 0xbin", Description: "How the hosted 0xbin service handles information, privacy requests, and reports of misuse."},
}

// Server owns the configured HTTP server lifecycle.
type Server struct {
	server *http.Server
}

// NewServer creates the HTTP server. Database readiness is deliberately not
// wired until Step 2.
func NewServer(cfg config.Config, pastes PasteService, readiness ...func(context.Context) error) *Server {
	return newServer(cfg, pastes, nil, readiness...)
}

// NewServerWithFrontend creates a server that serves the embedded frontend
// for browser routes while keeping API and health routes separate.
func NewServerWithFrontend(cfg config.Config, pastes PasteService, frontend fs.FS, readiness ...func(context.Context) error) *Server {
	return newServer(cfg, pastes, frontend, readiness...)
}

func newServer(cfg config.Config, pastes PasteService, frontend fs.FS, readiness ...func(context.Context) error) *Server {
	var ready func(context.Context) error
	if len(readiness) > 0 {
		ready = readiness[0]
	}
	return &Server{server: &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newHandler(cfg, pastes, frontend, ready),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}}
}

// Serve accepts HTTP traffic on listener.
func (s *Server) Serve(listener net.Listener) error {
	return s.server.Serve(listener)
}

// Shutdown gracefully stops accepting requests and waits for active requests.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// NewHandler creates the root router and its foundational middleware.
func NewHandler(cfg config.Config, pastes PasteService, readiness ...func(context.Context) error) http.Handler {
	return newHandler(cfg, pastes, nil, readiness...)
}

// NewHandlerWithFrontend attaches a frontend filesystem to browser routes.
// It is useful for integration tests and the embedded production bundle.
func NewHandlerWithFrontend(cfg config.Config, pastes PasteService, frontend fs.FS, readiness ...func(context.Context) error) http.Handler {
	return newHandler(cfg, pastes, frontend, readiness...)
}

func newHandler(cfg config.Config, pastes PasteService, frontend fs.FS, readiness ...func(context.Context) error) http.Handler {
	var ready func(context.Context) error
	if len(readiness) > 0 {
		ready = readiness[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) { notReady(w, r, ready) })
	if pastes != nil {
		limits, err := ratelimit.NewRegistry(map[ratelimit.Category]config.Rate{
			ratelimit.Create:  cfg.CreateRate,
			ratelimit.Read:    cfg.ReadRate,
			ratelimit.Miss:    cfg.MissRate,
			ratelimit.Consume: cfg.ConsumeRate,
		}, 10_000, 2*time.Hour, time.Now)
		if err != nil {
			panic("validated rate limit configuration is invalid: " + err.Error())
		}
		api := pasteAPI{pastes: pastes, baseURL: cfg.BaseURL, maxContentBytes: cfg.MaxPasteBytes, limits: limits}
		mux.HandleFunc("POST /api/v1/pastes", api.create)
		mux.HandleFunc("GET /api/v1/pastes/{slug}", api.get)
		mux.HandleFunc("POST /api/v1/pastes/{slug}/consume", api.consume)
		mux.HandleFunc("GET /api/v1/pastes/{slug}/raw", api.raw)
	}
	mux.HandleFunc("/api/", apiNotFound)
	mux.HandleFunc("/api", apiNotFound)
	if frontend == nil {
		mux.HandleFunc("/", notFound)
	} else {
		publicURL := strings.TrimSuffix(cfg.BaseURL.String(), "/")
		hostedService := strings.EqualFold(cfg.BaseURL.Hostname(), hostedDomain)
		mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
			serveRobots(w, r, publicURL)
		})
		mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
			serveSitemap(w, r, publicURL, hostedService)
		})
		mux.Handle("/", frontendHandler(frontend, publicURL, hostedService))
	}
	return requestID(recoverPanics(clientIP(mux, cfg.TrustedProxies)))
}

func frontendHandler(bundle fs.FS, publicURL string, hostedService bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." {
			name = ""
		}
		if name == "index.html" {
			http.Redirect(w, r, "/", http.StatusPermanentRedirect)
			return
		}
		if name != "" && fs.ValidPath(name) {
			if contents, err := fs.ReadFile(bundle, name); err == nil {
				serveFrontendFile(w, r, name, contents)
				return
			}
		}

		// Vite assets must exist exactly. All other browser paths are React
		// routes and receive the application shell.
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		contents, err := fs.ReadFile(bundle, "index.html")
		if err != nil {
			http.Error(w, "Frontend bundle is unavailable; run make build.", http.StatusServiceUnavailable)
			return
		}
		contents = injectHostedService(contents, hostedService)
		if page, ok := publicPage(name, hostedService); ok {
			contents = injectPageSEO(contents, publicURL+page.Path, page)
		} else {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
			contents = injectNoindex(contents)
		}
		serveFrontendFile(w, r, "index.html", contents)
	})
}

func publicPage(name string, hostedService bool) (publicPageSEO, bool) {
	if name == "" {
		return homepageSEO, true
	}
	if !hostedService {
		return publicPageSEO{}, false
	}
	for _, page := range hostedPublicPages {
		if strings.TrimPrefix(page.Path, "/") == name {
			return page, true
		}
	}
	return publicPageSEO{}, false
}

func injectHostedService(contents []byte, hostedService bool) []byte {
	value := "false"
	if hostedService {
		value = "true"
	}
	return bytes.Replace(contents, []byte(`data-hosted-service="false"`), []byte(`data-hosted-service="`+value+`"`), 1)
}

func injectNoindex(contents []byte) []byte {
	return bytes.Replace(contents, []byte(`<meta name="robots" content="index, follow" />`), []byte(`<meta name="robots" content="noindex, nofollow, noarchive" />`), 1)
}

func injectPageSEO(contents []byte, canonicalURL string, page publicPageSEO) []byte {
	contents = bytes.ReplaceAll(contents, []byte(defaultPageTitle), []byte(html.EscapeString(page.Title)))
	contents = bytes.ReplaceAll(contents, []byte(defaultPageDescription), []byte(html.EscapeString(page.Description)))
	escapedURL := html.EscapeString(canonicalURL)
	metadata := []byte(`<link rel="canonical" href="` + escapedURL + `" />
    <meta property="og:url" content="` + escapedURL + `" />`)
	if page.Application {
		structuredData, err := json.Marshal(struct {
			Context             string `json:"@context"`
			Type                string `json:"@type"`
			Name                string `json:"name"`
			URL                 string `json:"url"`
			Description         string `json:"description"`
			ApplicationCategory string `json:"applicationCategory"`
			OperatingSystem     string `json:"operatingSystem"`
		}{
			Context:             "https://schema.org",
			Type:                "WebApplication",
			Name:                "0xbin",
			URL:                 canonicalURL,
			Description:         "An ephemeral paste service with memorable links, automatic expiry, and optional client-side encryption.",
			ApplicationCategory: "DeveloperApplication",
			OperatingSystem:     "Any",
		})
		if err == nil {
			metadata = append(metadata, []byte("\n    <script type=\"application/ld+json\">"+string(structuredData)+"</script>")...)
		}
	}
	const marker = "<!-- OXBIN_RUNTIME_SEO -->"
	if bytes.Contains(contents, []byte(marker)) {
		return bytes.Replace(contents, []byte(marker), metadata, 1)
	}
	return bytes.Replace(contents, []byte("</head>"), append(metadata, []byte("\n  </head>")...), 1)
}

func serveRobots(w http.ResponseWriter, r *http.Request, publicURL string) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + publicURL + "/sitemap.xml\n"))
}

func serveSitemap(w http.ResponseWriter, r *http.Request, publicURL string, hostedService bool) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	var body strings.Builder
	body.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	body.WriteString("<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	pages := []publicPageSEO{homepageSEO}
	if hostedService {
		pages = append(pages, hostedPublicPages...)
	}
	for _, page := range pages {
		body.WriteString("  <url><loc>")
		body.WriteString(html.EscapeString(publicURL + page.Path))
		body.WriteString("</loc></url>\n")
	}
	body.WriteString("</urlset>\n")
	_, _ = w.Write([]byte(body.String()))
}

func serveFrontendFile(w http.ResponseWriter, r *http.Request, name string, contents []byte) {
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(contents)
}

func live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notReady(w http.ResponseWriter, r *http.Request, ready func(context.Context) error) {
	if ready != nil && ready(r.Context()) == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	writeError(w, http.StatusServiceUnavailable, "service_not_ready", "Service is not ready", requestIDFromContext(r.Context()))
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Not found", requestIDFromContext(r.Context()))
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Not found", requestIDFromContext(r.Context()))
}

type requestIDKey struct{}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "unavailable"
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deferred := &responseWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("recovered panic")
				if !deferred.wroteHeader {
					writeError(deferred, http.StatusInternalServerError, "internal_error", "Internal server error", requestIDFromContext(r.Context()))
				}
			}
		}()
		next.ServeHTTP(deferred, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
