package core

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"appfactory/internal/bus"
	"appfactory/internal/storage/storagetest"
)

// TestHealthReportsPostgres (postgres-tenancy 1.1): GET /health, what `factory doctor` prints, names Core's
// Postgres and company, and answers 503 once the database is unreachable.
func TestHealthReportsPostgres(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	s := storagetest.Open(t)
	c := &Core{Store: s, Bus: b, Token: "owner", Company: "default", DefaultReport: "manager"}
	h := c.Handler()
	get := func() (int, map[string]string) {
		r := httptest.NewRequest("GET", "/health", nil)
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var v map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return w.Code, v
	}
	if code, v := get(); code != 200 || !strings.HasPrefix(v["database"], "postgres ") || v["company"] != "default" || v["status"] != "ok" || v["explicit_exits"] != "off" || v["default_report"] != "manager" {
		t.Fatal(code, v)
	}
	s.DB.Close()
	if code, _ := get(); code != 503 {
		t.Fatal("health without its database", code)
	}
}
