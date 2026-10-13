package core

import (
	"net/http/httptest"
	"testing"
)

func TestUnauthorized(t *testing.T) {
	c := &Core{Token: "secret"}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/runs", nil)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unauthorized access", w.Code)
		}
	}
}
