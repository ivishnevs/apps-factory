// Package claudetest is a fake per-run Claude shim for tests. It replays a recorded
// <name>.events.jsonl stream (internal/driver/claude/testdata) with the shim's stdin protocol.
package claudetest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Dir is the recorded stream directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata")
}

// Serve reads the launch line and passes it to launch, writes the stream up to its `session`
// line, waits for {"start": true}, and writes the rest. At the scenario's interrupt_after it
// waits for {"interrupt": true}. Interrupt or EOF before start ends like the shim: not_started.
func Serve(dir, name string, in io.Reader, out io.Writer, launch func(line []byte)) error {
	b, err := os.ReadFile(filepath.Join(dir, name+".events.jsonl"))
	if err != nil {
		return err
	}
	var events []json.RawMessage
	for dec := json.NewDecoder(bytes.NewReader(b)); dec.More(); {
		var e json.RawMessage
		if err = dec.Decode(&e); err != nil {
			return err
		}
		events = append(events, e)
	}
	var scenario struct {
		InterruptAfter *int `json:"interrupt_after"`
	}
	if b, err = os.ReadFile(filepath.Join(dir, name+".json")); err == nil {
		_ = json.Unmarshal(b, &scenario)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	if !sc.Scan() {
		return fmt.Errorf("no launch line")
	}
	launch(append([]byte(nil), sc.Bytes()...))
	control := make(chan string, 4)
	go func() {
		for sc.Scan() {
			var c struct{ Start, Interrupt bool }
			if json.Unmarshal(sc.Bytes(), &c) == nil && c.Start {
				control <- "start"
			} else if c.Interrupt {
				control <- "interrupt"
			}
		}
		close(control)
	}()
	activity := 0
	for _, e := range events {
		var ev struct{ Event, ID string }
		_ = json.Unmarshal(e, &ev)
		if ev.Event == "activity" {
			if scenario.InterruptAfter != nil && activity == *scenario.InterruptAfter {
				for c := range control {
					if c == "interrupt" {
						break
					}
				}
			}
			activity++
		}
		if _, err = out.Write(append(e, '\n')); err != nil {
			return err
		}
		if ev.Event == "session" {
			if c, ok := <-control; !ok || c != "start" {
				_, err = fmt.Fprintf(out, "%s\n%s\n", `{"event":"error","class":"not_started","message":"interrupted before start"}`, `{"event":"result","status":"UNKNOWN","result":"interrupted before start","session_id":"`+ev.ID+`"}`)
				return err
			}
		}
	}
	return nil
}
