// Package rpc implements the official Codex newline-delimited app-server protocol.
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"appfactory/internal/procgroup"
)

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}
type Client struct {
	cmd *exec.Cmd
	// group is the runtime's process group (leader start time); zero when it could not be recorded.
	group   procgroup.Group
	in      io.WriteCloser
	write   sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Message
	next    atomic.Int64
	Events  chan Message
	done    chan struct{}
	wait    chan error
	err     error
	once    sync.Once
}

func Start(ctx context.Context, bin string, args ...string) (*Client, error) {
	return StartEnv(ctx, bin, args, nil)
}

// StartEnv supplies a per-runtime environment without mutating the worker process.
func StartEnv(ctx context.Context, bin string, args []string, env []string) (*Client, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, in: in, pending: map[string]chan Message{}, Events: make(chan Message, 1024), done: make(chan struct{}), wait: make(chan error, 1)}
	// Recorded before Wait can reap the runtime, so Close never signals a group that reused its PID.
	c.group, _ = procgroup.Record(cmd.Process.Pid)
	go c.read(out)
	go func() { c.wait <- cmd.Wait() }()
	return c, nil
}
func (c *Client) fail(err error) {
	c.once.Do(func() { c.mu.Lock(); c.err = err; c.mu.Unlock(); close(c.done) })
}
func (c *Client) read(out io.Reader) {
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 65536), 16<<20)
	for scanner.Scan() {
		var m Message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			c.fail(fmt.Errorf("RPC invalid JSON: %w", err))
			return
		}
		if len(m.ID) > 0 && m.Method == "" {
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
			continue
		}
		select {
		case c.Events <- m:
		default:
			c.fail(errors.New("RPC event buffer overflow"))
			return
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.fail(err)
}
func (c *Client) send(v any) error {
	c.write.Lock()
	defer c.write.Unlock()
	return json.NewEncoder(c.in).Encode(v)
}
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	id := c.next.Add(1)
	key := fmt.Sprint(id)
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	case m := <-ch:
		if len(m.Error) > 0 && string(m.Error) != "null" {
			return fmt.Errorf("RPC %s: %s", method, m.Error)
		}
		if result != nil {
			return json.Unmarshal(m.Result, result)
		}
		return nil
	}
}
func (c *Client) Notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}
func (c *Client) Respond(id json.RawMessage, result any, rpcErr any) error {
	v := map[string]any{"id": id}
	if rpcErr != nil {
		v["error"] = rpcErr
	} else {
		v["result"] = result
	}
	return c.send(v)
}
func (c *Client) Done() <-chan struct{} { return c.done }

// Pid is the runtime process ID, which is also its process group ID.
func (c *Client) Pid() int   { return c.cmd.Process.Pid }
func (c *Client) Err() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

// killLimit bounds how long Close waits for the killed runtime group to disappear.
const killLimit = 5 * time.Second

// Close kills the runtime's group only while it is still the recorded one: the leader may already be
// reaped and its PID reused by another group. Without a record it kills just the leader, which
// os.Process never signals after reaping.
func (c *Client) Close() {
	_ = c.in.Close()
	if c.group.ID > 1 {
		_ = c.group.Stop(0, killLimit)
	} else if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	<-c.wait
	c.fail(io.EOF)
}
