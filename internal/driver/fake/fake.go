// Package fake replays scripted runtime sessions so executor tests run without a model.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"appfactory/internal/driver"
	"appfactory/internal/protocol"
)

// Script is a recorded session. Without Status the session runs until interrupted and then
// ends CANCELLED; with Unknown it ends without terminal evidence.
type Script struct {
	SessionID string     `json:"session_id"`
	Activity  []Activity `json:"activity"`
	Status    string     `json:"status"`
	Text      string     `json:"text"`
	Unknown   string     `json:"unknown"`
	LaunchErr string     `json:"launch_error"`
}
type Activity struct {
	Method string          `json:"method"`
	Data   json.RawMessage `json:"data"`
}

func Load(path string) (Script, error) {
	var s Script
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}

type Driver struct {
	Provider protocol.RuntimeProvider
	Script   Script
	MCP      bool

	mu       sync.Mutex
	launches []driver.Launch
}

func (d *Driver) Name() protocol.RuntimeProvider {
	if d.Provider == "" {
		return protocol.RuntimeCodex
	}
	return d.Provider
}
func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Resume: true, MCP: d.MCP, Activity: true}
}
func (d *Driver) CheckVersion(context.Context) (string, error) { return "fake", nil }

// Launches returns the launch parameters seen so far.
func (d *Driver) Launches() []driver.Launch {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]driver.Launch{}, d.launches...)
}
func (d *Driver) Launch(_ context.Context, l driver.Launch) (driver.Session, error) {
	d.mu.Lock()
	d.launches = append(d.launches, l)
	d.mu.Unlock()
	if d.Script.LaunchErr != "" {
		return nil, errors.New(d.Script.LaunchErr)
	}
	id := d.Script.SessionID
	if id == "" {
		id = l.Spec.SessionID
	}
	if id == "" {
		id = "fake-session"
	}
	return &session{script: d.Script, id: id, events: make(chan driver.Event, len(d.Script.Activity)), done: make(chan driver.Result, 1), interrupt: make(chan struct{}), stop: make(chan struct{})}, nil
}

type session struct {
	script                  Script
	id                      string
	events                  chan driver.Event
	done                    chan driver.Result
	interrupt, stop         chan struct{}
	interruptOnce, stopOnce sync.Once
}

func (s *session) ID() string                  { return s.id }
func (s *session) Events() <-chan driver.Event { return s.events }
func (s *session) Done() <-chan driver.Result  { return s.done }
func (s *session) Start(context.Context) error {
	for _, a := range s.script.Activity {
		s.events <- driver.Event{Kind: driver.EventActivity, Method: a.Method, Data: a.Data}
	}
	go func() {
		r := s.result()
		close(s.events)
		s.done <- r
	}()
	return nil
}
func (s *session) result() driver.Result {
	switch {
	case s.script.Unknown != "":
		return driver.Result{Err: errors.New(s.script.Unknown)}
	case s.script.Status != "":
		return driver.Result{Status: s.script.Status, Text: s.script.Text}
	}
	select {
	case <-s.interrupt:
		return driver.Result{Status: "CANCELLED", Text: s.script.Text}
	case <-s.stop:
		return driver.Result{Err: errors.New("session closed")}
	}
}
func (s *session) Interrupt(context.Context) error {
	s.interruptOnce.Do(func() { close(s.interrupt) })
	return nil
}
func (s *session) ProcessGroup() int { return 0 }
func (s *session) Close()            { s.stopOnce.Do(func() { close(s.stop) }) }
