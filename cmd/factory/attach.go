package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"appfactory/internal/messaging"
	"appfactory/internal/worker"
)

// splitAttach separates trailing "--attach FILE..." from a command's arguments (people-attachments).
func splitAttach(args []string) ([]string, []string) {
	for i, a := range args {
		if a == "--attach" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// uploadImages uploads each file as a person's image (POST /uploads) and answers the attachments to name.
func uploadImages(ctx context.Context, api worker.API, files []string) ([]messaging.Attachment, error) {
	out := make([]messaging.Attachment, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var a messaging.Attachment
		if err = api.Upload(ctx, "/uploads?name="+url.QueryEscape(filepath.Base(f)), http.DetectContentType(b), b, &a); err != nil {
			return nil, fmt.Errorf("upload %s: %w", f, err)
		}
		out = append(out, a)
	}
	return out, nil
}
