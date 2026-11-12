package core

import (
	"context"
	"io"
	"path"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
)

// telegramPhotoMax is Telegram's limit for a photo; a larger image without a preview stays in the inbox.
const telegramPhotoMax = 10 << 20

// photoExts are the images Telegram shows as photos.
var photoExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true}

// Images are the images a question or a decision proposal carries (inbox-visual-previews 2.3), in the order given
// with their captions: the preview of a large image, else the original. more counts the images not sent (beyond
// one album, too large or unreadable).
func (b telegramBackend) Images(ctx context.Context, id string) ([]telegram.Photo, int, error) {
	m, err := b.c.Store.Message(ctx, b.company, id)
	if err != nil {
		return nil, 0, err
	}
	type image struct {
		name, caption, sha string
		size               int64
		preview            *messaging.Preview
	}
	var images []image
	for _, a := range m.Attachments {
		images = append(images, image{path.Base(a.Path), a.Caption, a.SHA256, a.Size, a.Preview})
	}
	for _, r := range m.Materials {
		if r.SHA256 != "" {
			images = append(images, image{r.Name, r.Caption, r.SHA256, r.Size, r.Preview})
		}
	}
	var photos []telegram.Photo
	more := 0
	for _, im := range images {
		if !photoExts[strings.ToLower(path.Ext(im.name))] {
			continue
		}
		sha, size, name := im.sha, im.size, im.name
		if im.preview != nil {
			sha, size, name = im.preview.SHA256, im.preview.Size, "preview-"+name
		}
		if len(photos) >= telegram.MaxAlbum || size > telegramPhotoMax {
			more++
			continue
		}
		rc, _, err := b.c.Store.OpenBlob(ctx, b.company, sha)
		if err != nil {
			more++
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, telegramPhotoMax+1))
		rc.Close()
		if err != nil || len(data) > telegramPhotoMax {
			more++
			continue
		}
		photos = append(photos, telegram.Photo{Name: name, Caption: im.caption, Data: data})
	}
	return photos, more, nil
}
