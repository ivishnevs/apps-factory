package core

import (
	"context"
	"encoding/json"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"appfactory/internal/factorytools"
	"appfactory/internal/preview"
)

// fakeAlbums is the Bot API's photo methods (sendPhoto, sendMediaGroup: multipart uploads) in front of fakeBot,
// which answers everything else. Each album records the captions, the uploaded files and how many text messages
// were sent before it.
type fakeAlbums struct {
	bot    *fakeBot
	mu     sync.Mutex
	albums []fakeAlbum
}

type fakeAlbum struct {
	method   string
	chat     string
	captions []string
	files    [][]byte
	after    int
}

func (f *fakeAlbums) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if method != "sendPhoto" && method != "sendMediaGroup" {
		f.bot.ServeHTTP(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/botTEST-TOKEN/") || r.ParseMultipartForm(64<<20) != nil {
		http.Error(w, `{"ok":false,"description":"Bad Request"}`, 400)
		return
	}
	a := fakeAlbum{method: method, chat: r.FormValue("chat_id"), after: len(f.bot.texts())}
	read := func(field string) []byte {
		file, _, err := r.FormFile(field)
		if err != nil {
			return nil
		}
		defer file.Close()
		b := make([]byte, 0, 1024)
		buf := make([]byte, 32<<10)
		for {
			n, err := file.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				return b
			}
		}
	}
	if method == "sendPhoto" {
		a.captions, a.files = []string{r.FormValue("caption")}, [][]byte{read("photo")}
	} else {
		var media []struct{ Type, Media, Caption string }
		if json.Unmarshal([]byte(r.FormValue("media")), &media) != nil {
			http.Error(w, `{"ok":false,"description":"media"}`, 400)
			return
		}
		for _, m := range media {
			a.captions = append(a.captions, m.Caption)
			a.files = append(a.files, read(strings.TrimPrefix(m.Media, "attach://")))
		}
	}
	f.mu.Lock()
	f.albums = append(f.albums, a)
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
}

// TestTelegramQuestionAlbum (inbox-visual-previews 2.3, 4.1 with a fake Bot API): the producer's verdict question
// with the screenshots classic, v1 and v2 reaches Telegram as an album in that order with the captions, right
// before the question with its option buttons; the large screenshot goes as its preview. A question without images
// sends no album.
func TestTelegramQuestionAlbum(t *testing.T) {
	th := newTelegramHarness(t)
	ctx := context.Background()
	albums := &fakeAlbums{bot: th.bot}
	srv := httptest.NewServer(albums)
	defer srv.Close()
	th.n.API.BaseURL = srv.URL
	core := httptest.NewServer(th.c.Handler())
	defer core.Close()

	work := t.TempDir()
	writePNG(t, filepath.Join(work, "shots/classic.png"), 300, 200, color.RGBA{200, 0, 0, 255})
	writePNG(t, filepath.Join(work, "shots/v1.png"), 300, 200, color.RGBA{0, 200, 0, 255})
	writePNG(t, filepath.Join(work, "shots/v2.png"), 3000, 2000, color.RGBA{0, 0, 200, 255})
	th.create("vp", "notes", "demo")
	r := th.start("vp")
	a := &factorytools.Adapter{URL: core.URL, Token: r.token, Workdir: work}
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{
		"body":      "Storm Towers v2: which towers ship?",
		"questions": []any{map[string]any{"id": "verdict", "type": "single_choice", "question": "Verdict", "options": []any{"v2", "v1", "classic"}}},
		"attachments": []any{
			map[string]any{"path": "shots/classic.png", "caption": "classic"},
			map[string]any{"path": "shots/v1.png", "caption": "v1"},
			map[string]any{"path": "shots/v2.png", "caption": "v2"},
		},
		"request_id": "verdict", "brief": testBrief()}}); err != nil {
		t.Fatal(err)
	}
	th.tick()
	if len(albums.albums) != 1 {
		t.Fatalf("albums %+v", albums.albums)
	}
	al := albums.albums[0]
	if al.method != "sendMediaGroup" || al.chat != "4242" || strings.Join(al.captions, "|") != "classic|v1|v2" || al.after != 0 || len(al.files) != 3 {
		t.Fatalf("%s %s %v after %d", al.method, al.chat, al.captions, al.after)
	}
	cfg, err := jpeg.DecodeConfig(strings.NewReader(string(al.files[2])))
	if err != nil || cfg.Width != preview.MaxSide {
		t.Fatalf("v2 went as %+v %v", cfg, err)
	}
	texts := th.bot.texts()
	if len(texts) != 1 || !strings.Contains(texts[0], "Storm Towers v2") || strings.Contains(texts[0], "inbox") {
		t.Fatal(texts)
	}
	markup, _ := json.Marshal(th.bot.last()["reply_markup"])
	if !strings.Contains(string(markup), `"text":"classic"`) {
		t.Fatal(string(markup))
	}

	// A question without images: text only.
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_owner_ask", Arguments: map[string]any{"body": "Ship on Friday?", "request_id": "plain"}}); err != nil {
		t.Fatal(err)
	}
	th.tick()
	if len(albums.albums) != 1 || len(th.bot.texts()) != 2 {
		t.Fatal(len(albums.albums), th.bot.texts())
	}
}
