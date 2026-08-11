package httpapi

import (
	"context"
	"encoding/json"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/0atxl/0xbin/internal/config"
)

const testIndexHTML = `<html lang="en" data-hosted-service="false" data-live-enabled="true"><head>
<meta name="description" content="Create temporary text and code pastes with memorable links, automatic expiry, and optional client-side encryption." />
<meta name="robots" content="index, follow" />
<meta property="og:title" content="0xbin — Ephemeral Paste Service" />
<!-- OXBIN_RUNTIME_SEO -->
<title>0xbin — Ephemeral Paste Service</title>
</head><body><div id="root"></div></body></html>`

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()
	handler := NewHandler(testConfig(t), nil)

	t.Run("liveness", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
		}
		if got := recorder.Header().Get("X-Request-ID"); got == "" {
			t.Error("missing X-Request-ID")
		}
	})

	t.Run("readiness is reserved", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		assertError(t, recorder, http.StatusServiceUnavailable, "service_not_ready")
	})
}

func TestReadinessUsesDatabaseCheck(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	NewHandler(testConfig(t), nil, func(context.Context) error { return nil }).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestUnknownAPIRouteReturnsJSONError(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	NewHandler(testConfig(t), nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	assertError(t, recorder, http.StatusNotFound, "not_found")
}

func TestFrontendRoutesAndAssets(t *testing.T) {
	t.Parallel()
	bundle := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte(testIndexHTML)},
		"assets/app.js":  &fstest.MapFile{Data: []byte("console.log('0xbin')")},
		"assets/app.css": &fstest.MapFile{Data: []byte("body{}")},
	}
	handler := NewHandlerWithFrontend(testConfig(t), nil, bundle)

	homepage := httptest.NewRecorder()
	handler.ServeHTTP(homepage, httptest.NewRequest(http.MethodGet, "/", nil))
	if homepage.Code != http.StatusOK {
		t.Fatalf("homepage status = %d, want 200", homepage.Code)
	}
	if got := homepage.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("homepage Cache-Control = %q, want no-store", got)
	}
	if got := homepage.Header().Get("X-Robots-Tag"); got != "" {
		t.Errorf("homepage X-Robots-Tag = %q, want empty", got)
	}
	if got := homepage.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("homepage CSP = %q", got)
	}
	for header, want := range map[string]string{
		"Referrer-Policy":        "same-origin",
		"Permissions-Policy":     "camera=(), geolocation=(), microphone=(), payment=(), usb=()",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := homepage.Header().Get(header); got != want {
			t.Errorf("homepage %s = %q, want %q", header, got, want)
		}
	}
	if got := homepage.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HTTP homepage HSTS = %q, want empty", got)
	}
	for _, expected := range []string{
		`rel="canonical" href="http://localhost:8080/"`,
		`property="og:url" content="http://localhost:8080/"`,
		`"@type":"WebApplication"`,
	} {
		if !strings.Contains(homepage.Body.String(), expected) {
			t.Errorf("homepage is missing %q", expected)
		}
	}

	pastePage := httptest.NewRecorder()
	handler.ServeHTTP(pastePage, httptest.NewRequest(http.MethodGet, "/radiantcolorfulpomeranian", nil))
	if pastePage.Code != http.StatusOK {
		t.Fatalf("paste page status = %d, want 200", pastePage.Code)
	}
	if got := pastePage.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("paste page Cache-Control = %q, want no-store", got)
	}
	if got := pastePage.Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
		t.Errorf("paste page X-Robots-Tag = %q", got)
	}
	if !strings.Contains(pastePage.Body.String(), `content="noindex, nofollow, noarchive"`) {
		t.Error("paste page HTML is missing noindex metadata")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset Cache-Control = %q", got)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if recorder.Code != http.StatusPermanentRedirect || recorder.Header().Get("Location") != "/" {
		t.Errorf("index redirect = %d %q, want 308 /", recorder.Code, recorder.Header().Get("Location"))
	}
}

func TestSecurityHeadersEnableHSTSOnlyForHTTPS(t *testing.T) {
	cfg := testConfig(t)
	cfg.BaseURL.Scheme = "https"
	recorder := httptest.NewRecorder()
	NewHandler(cfg, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := recorder.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("HTTPS HSTS = %q", got)
	}
}

func TestBarePasteModeOmitsLiveRoutesAndFrontendEntry(t *testing.T) {
	t.Parallel()
	bundle := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(testIndexHTML)},
	}
	cfg := testConfig(t)
	cfg.LiveEnabled = false
	handler := newHandlerWithLive(cfg, nil, bundle, nil)

	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodPost, "/api/v1/live", nil))
	assertError(t, api, http.StatusNotFound, "not_found")

	room := httptest.NewRecorder()
	handler.ServeHTTP(room, httptest.NewRequest(http.MethodGet, "/live/calmbrightotter", nil))
	if room.Code != http.StatusOK {
		t.Fatalf("live browser route status = %d, want application unavailable boundary", room.Code)
	}
	if !strings.Contains(room.Body.String(), `data-live-enabled="false"`) {
		t.Fatalf("bare-paste shell still enables LiveBin: %s", room.Body.String())
	}
}

func TestSearchDiscoveryFilesAdvertiseOnlyHomepage(t *testing.T) {
	t.Parallel()
	bundle := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(testIndexHTML)},
	}
	handler := NewHandlerWithFrontend(testConfig(t), nil, bundle)

	robots := httptest.NewRecorder()
	handler.ServeHTTP(robots, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if robots.Code != http.StatusOK {
		t.Fatalf("robots status = %d, want 200", robots.Code)
	}
	if got := robots.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("robots Content-Type = %q", got)
	}
	if got := robots.Body.String(); got != "User-agent: *\nAllow: /\nSitemap: http://localhost:8080/sitemap.xml\n" {
		t.Errorf("robots body = %q", got)
	}

	sitemap := httptest.NewRecorder()
	handler.ServeHTTP(sitemap, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if sitemap.Code != http.StatusOK {
		t.Fatalf("sitemap status = %d, want 200", sitemap.Code)
	}
	if got := sitemap.Header().Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Errorf("sitemap Content-Type = %q", got)
	}
	if strings.Count(sitemap.Body.String(), "<loc>") != 1 ||
		!strings.Contains(sitemap.Body.String(), "<loc>http://localhost:8080/</loc>") {
		t.Errorf("sitemap must contain only the configured homepage: %q", sitemap.Body.String())
	}

	selfHostedPolicy := httptest.NewRecorder()
	handler.ServeHTTP(selfHostedPolicy, httptest.NewRequest(http.MethodGet, "/privacy", nil))
	if got := selfHostedPolicy.Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
		t.Errorf("self-hosted policy-like route X-Robots-Tag = %q", got)
	}
	if strings.Contains(selfHostedPolicy.Body.String(), `data-hosted-service="true"`) {
		t.Error("self-hosted shell must not enable hosted policy navigation")
	}
}

func TestHostedPublicPagesAreIndexableAndDiscoverable(t *testing.T) {
	t.Parallel()
	bundle := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(testIndexHTML)},
	}
	cfg := testConfig(t)
	cfg.BaseURL = &url.URL{Scheme: "https", Host: "0xbin.app"}
	handler := NewHandlerWithFrontend(cfg, nil, bundle)

	for _, page := range hostedPublicPages {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, page.Path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", page.Path, recorder.Code)
		}
		if got := recorder.Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("GET %s X-Robots-Tag = %q, want empty", page.Path, got)
		}
		for _, expected := range []string{
			`data-hosted-service="true"`,
			`rel="canonical" href="https://0xbin.app` + page.Path + `"`,
			`<title>` + html.EscapeString(page.Title) + `</title>`,
			`content="` + html.EscapeString(page.Description) + `"`,
		} {
			if !strings.Contains(recorder.Body.String(), expected) {
				t.Errorf("GET %s is missing %q", page.Path, expected)
			}
		}
	}

	sitemap := httptest.NewRecorder()
	handler.ServeHTTP(sitemap, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if strings.Count(sitemap.Body.String(), "<loc>") != 1+len(hostedPublicPages) {
		t.Fatalf("hosted sitemap entries = %d, want %d", strings.Count(sitemap.Body.String(), "<loc>"), 1+len(hostedPublicPages))
	}
	for _, page := range append([]publicPageSEO{homepageSEO}, hostedPublicPages...) {
		if !strings.Contains(sitemap.Body.String(), "<loc>https://0xbin.app"+page.Path+"</loc>") {
			t.Errorf("hosted sitemap is missing %s", page.Path)
		}
	}

	pastePage := httptest.NewRecorder()
	handler.ServeHTTP(pastePage, httptest.NewRequest(http.MethodGet, "/quietbrightotter", nil))
	if got := pastePage.Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
		t.Errorf("hosted paste X-Robots-Tag = %q", got)
	}
	if !strings.Contains(pastePage.Body.String(), `data-hosted-service="true"`) {
		t.Error("hosted paste shell must retain the hosted menu marker")
	}
}

func TestRecoveryReturnsStableError(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	handler := requestID(recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	})))
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test", nil))
	assertError(t, recorder, http.StatusInternalServerError, "internal_error")
}

func TestServerShutdownHonorsContext(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	server := NewServer(cfg, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	deadline := time.Now().Add(time.Second)
	for {
		connection, dialErr := net.DialTimeout("tcp", listener.Addr().String(), 10*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", dialErr)
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-serveErr; err != ErrServerClosed {
		t.Fatalf("Serve() error = %v, want %v", err, ErrServerClosed)
	}
}

func assertError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	var response Error
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", response.Error.Code, wantCode)
	}
	if response.Error.RequestID == "" {
		t.Error("error response missing request ID")
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
