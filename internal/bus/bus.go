package bus

import (
	"appfactory/internal/protocol"
	"context"
	"encoding/json"
	"errors"
	"github.com/nats-io/nats.go"
	"slices"
	"time"
)

// Streams are the factory's JetStream streams; every subject starts with factory.<company> (protocol
// CommandSubject, EventSubject, FeedSubject). Node control subjects stay outside the streams.
var Streams = []nats.StreamConfig{
	{Name: "FACTORY_COMMANDS", Subjects: []string{"factory.*.commands.>"}, Storage: nats.FileStorage},
	{Name: "FACTORY_EVENTS", Subjects: []string{"factory.*.events.>", "factory.*.feed.>"}, Storage: nats.FileStorage},
}

type Bus struct {
	NC *nats.Conn
	JS nats.JetStreamContext
}

// Connect dials url without client options (Core, tests); nodes use ConnectWith.
func Connect(url string) (*Bus, error) {
	nc, err := nats.Connect(url, nats.Name("appfactory"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
	if err != nil {
		return nil, err
	}
	return setup(nc)
}

// setup opens JetStream on nc and makes sure the command and event streams exist (StreamInfo only when they do).
func setup(nc *nats.Conn) (*Bus, error) {
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}
	b := &Bus{nc, js}
	for _, c := range Streams {
		var info *nats.StreamInfo
		if info, err = js.StreamInfo(c.Name); errors.Is(err, nats.ErrStreamNotFound) {
			_, err = js.AddStream(&c)
		} else if err == nil && !slices.Equal(info.Config.Subjects, c.Subjects) {
			// A stream of the unprefixed subjects (before company prefixes): take the company subjects.
			cfg := info.Config
			cfg.Subjects = c.Subjects
			_, err = js.UpdateStream(&cfg)
		}
		if err != nil {
			nc.Close()
			return nil, err
		}
	}
	return b, nil
}
func (b *Bus) Publish(ctx context.Context, subject, id string, body []byte) error {
	_, err := b.JS.Publish(subject, body, nats.MsgId(id), nats.Context(ctx))
	return err
}

// Notify publishes body on a plain subject outside the streams (a node's control subject): fan-out to the
// current subscribers, nothing is stored. The flush returns once the server has it.
func (b *Bus) Notify(ctx context.Context, subject string, body []byte) error {
	if err := b.NC.Publish(subject, body); err != nil {
		return err
	}
	return b.NC.FlushWithContext(ctx)
}
func (b *Bus) Event(ctx context.Context, company, kind, cause string, p protocol.Event) error {
	e := protocol.Wrap("agent."+kind, p.RunID, cause, p)
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return b.Publish(ctx, protocol.EventSubject(company, "agent."+kind), e.ID, body)
}
func (b *Bus) Subscribe(subject, durable string) (*nats.Subscription, error) {
	return b.JS.PullSubscribe(subject, durable, nats.ManualAck(), nats.AckExplicit(), nats.AckWait(60*time.Second), nats.MaxAckPending(1))
}
