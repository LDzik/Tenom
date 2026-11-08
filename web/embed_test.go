package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LDzik/Tenom/web"
)

func TestWebHandler_Index(t *testing.T) {
	handler := web.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Tenom") {
		t.Fatalf("Expected 'Tenom' in index.html, got: %s", body)
	}
	if !strings.Contains(body, "chart-ping") {
		t.Fatalf("Expected 'chart-ping' element in index.html")
	}
	if !strings.Contains(body, "echarts") {
		t.Fatalf("Expected 'echarts' reference in index.html")
	}
}

func TestWebHandler_StaticCSS(t *testing.T) {
	handler := web.Handler()

	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for CSS, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "--bg-app") {
		t.Fatalf("Expected CSS variable '--bg-app' in style.css")
	}
}

func TestWebHandler_StaticJS(t *testing.T) {
	handler := web.Handler()

	req := httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for JS, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "echarts") {
		t.Fatalf("Expected 'echarts' reference in app.js")
	}
	if !strings.Contains(body, "/api/v1/status") {
		t.Fatalf("Expected status endpoint in app.js")
	}
}

func TestWebHandler_SPAFallback(t *testing.T) {
	handler := web.Handler()

	req := httptest.NewRequest(http.MethodGet, "/unknown/route", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on SPA fallback, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "TENOM") {
		t.Fatalf("Expected SPA index.html on fallback route")
	}
}
