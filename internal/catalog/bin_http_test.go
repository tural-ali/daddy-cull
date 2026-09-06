package catalog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBinHTTPAuthorizationAndWorkflow(t *testing.T) {
	b, _, _ := binFixture(t)
	secret := strings.Repeat("a", 64)
	worker := httptest.NewServer(b.Handler(secret))
	defer worker.Close()
	denied := httptest.NewRecorder()
	b.Handler(secret).ServeHTTP(denied, httptest.NewRequest("GET", "/bin", nil))
	if denied.Code != 403 {
		t.Fatal("private worker has no authentication")
	}
	gateway := BinGateway(worker.URL, secret)
	request := func(path string, body any, origin string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "http://app.local/api/bin/"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		return w
	}
	if w := request("preview", map[string]any{"ids": []int{1}}, "http://evil.invalid"); w.Code != http.StatusForbidden {
		t.Fatal("cross-origin write accepted")
	}
	w := request("preview", map[string]any{"ids": []int{1}}, "http://app.local")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p BinPlan
	json.Unmarshal(w.Body.Bytes(), &p)
	w = request("execute", map[string]string{"id": p.ID, "action": "quarantine"}, "http://app.local")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.State != "bin" {
		t.Fatal("not in bin")
	}
	w = request("execute", map[string]string{"id": p.ID, "action": "restore"}, "http://app.local")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
