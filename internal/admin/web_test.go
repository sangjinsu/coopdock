package admin

import (
	"strings"
	"testing"
)

func TestEmbeddedAssetsAreProtectedAndExact(t *testing.T) {
	h, e := New(Config{ViewerToken: testToken}, testCollector(t, nil))
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ path, kind string }{{"/admin", "text/html; charset=utf-8"}, {"/admin/assets/dashboard.css", "text/css; charset=utf-8"}, {"/admin/assets/dashboard.js", "text/javascript; charset=utf-8"}} {
		get := adminRequest(h, "GET", tc.path, "valid")
		head := adminRequest(h, "HEAD", tc.path, "valid")
		if get.Code != 200 || head.Code != 200 || get.Header().Get("Content-Type") != tc.kind || get.Body.Len() == 0 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") {
			t.Errorf("asset contract %s", tc.path)
		}
	}
	html := adminRequest(h, "GET", "/admin", "valid").Body.String()
	for _, forbidden := range []string{"<style", "onclick=", "onload=", "http://", "https://", testToken} {
		if strings.Contains(html, forbidden) {
			t.Errorf("unsafe shell %s", forbidden)
		}
	}
	if !strings.Contains(html, `lang="ko"`) || !strings.Contains(html, `src="/admin/assets/dashboard.js"`) || !strings.Contains(html, `href="/admin/assets/dashboard.css"`) {
		t.Error("skeleton assets disconnected")
	}
	for _, path := range []string{"/admin/assets/", "/admin/index.html", "/admin/web/index.html", "/admin/assets/index.html", "/admin/assets/%2e%2e/index.html", "/admin/api/detail"} {
		if w := adminRequest(h, "GET", path, "valid"); w.Code != 404 {
			t.Errorf("fallback/listing/traversal %s=%d", path, w.Code)
		}
	}
}
