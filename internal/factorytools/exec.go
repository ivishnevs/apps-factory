package factorytools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runIn runs a command in dir with a bounded time and returns its trimmed stdout; a failure carries stderr.
func runIn(ctx context.Context, dir, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", &notFound{name}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return out, fmt.Errorf("%s %s: exit %d: %s", name, strings.Join(args, " "), exit.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return out, err
	}
	return out, nil
}
