package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A blob upload slower than the server's read timeout still arrives whole once the handler extends it.
func TestBlobTransferOutlivesServerTimeouts(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blobTransfer(w)
		b, err := io.ReadAll(r.Body)
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		got <- string(b)
	}))
	srv.Config.ReadTimeout = 50 * time.Millisecond
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		for _, part := range []string{"slow ", "blob ", "body"} {
			_, _ = pw.Write([]byte(part))
			time.Sleep(60 * time.Millisecond)
		}
		pw.Close()
	}()
	req, _ := http.NewRequest(http.MethodPut, srv.URL, pr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if body := <-got; !strings.HasPrefix(body, "slow blob body") {
		t.Fatalf("body = %q", body)
	}
}
