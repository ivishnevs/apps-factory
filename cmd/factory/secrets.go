package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"

	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// secretsCommand is `factory secrets set NAME [--file PATH] | list | delete NAME | rotate-master` (core-secret-store
// D2). A value comes from stdin or a file, never from an argument, and nothing prints a value.
func secretsCommand(ctx context.Context, api worker.API, args []string, stdin io.Reader) error {
	usage := errors.New("usage: secrets set NAME [--file PATH] (string value on stdin) | secrets list | secrets needed | secrets delete NAME | secrets rotate-master")
	method, path := "GET", "/secrets"
	var body any
	switch {
	case len(args) == 2 && args[1] == "list":
	case len(args) == 2 && args[1] == "needed":
		path = "/secrets/needed"
	case len(args) == 3 && args[1] == "delete":
		method, path = "DELETE", "/secrets/"+url.PathEscape(args[2])
	case len(args) == 2 && args[1] == "rotate-master":
		method, path = "POST", "/secrets/rotate-master"
	case (len(args) == 3 || len(args) == 5 && args[3] == "--file") && args[1] == "set":
		p := core.SecretPut{Kind: storage.SecretString}
		if len(args) == 5 {
			b, err := os.ReadFile(args[4])
			if err != nil {
				return err
			}
			p.Kind, p.Value = storage.SecretFile, base64.StdEncoding.EncodeToString(b)
		} else {
			b, err := io.ReadAll(io.LimitReader(stdin, storage.MaxSecretBytes+1))
			if err != nil {
				return err
			}
			p.Value = strings.TrimRight(string(b), "\r\n") // `echo value |` and a terminal's Enter end with a newline
		}
		method, path, body = "PUT", "/secrets/"+url.PathEscape(args[2]), p
	default:
		return usage
	}
	var v any
	if err := api.Do(ctx, method, path, body, &v); err != nil {
		return err
	}
	return printJSON(v)
}
