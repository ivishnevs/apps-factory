package bus

import (
	"appfactory/internal/protocol"
	"context"
	"encoding/json"
	"errors"
	"github.com/nats-io/nats.go"
	"time"
)

type Bus struct {
	NC *nats.Conn
	JS nats.JetStreamContext
}

func Connect(url string) (*Bus, error) {
	nc, err := nats.Connect(url, nats.Name("appfactory"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
	if err != nil {
		return nil, err
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}
	b := &Bus{nc, js}
	for _, c := range []nats.StreamConfig{{Name: "FACTORY_COMMANDS", Subjects: []string{"factory.commands.>"}, Storage: nats.FileStorage}, {Name: "FACTORY_EVENTS", Subjects: []string{"factory.events.>"}, Storage: nats.FileStorage}} {
		if _, err = js.StreamInfo(c.Name); errors.Is(err, nats.ErrStreamNotFound) {
			_, err = js.AddStream(&c)
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
func (b *Bus) Event(ctx context.Context, kind, cause string, p protocol.Event) error {
	e := protocol.Wrap("agent."+kind, p.RunID, cause, p)
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return b.Publish(ctx, "factory.events.agent."+kind, e.ID, body)
}
func (b *Bus) Subscribe(subject, durable string) (*nats.Subscription, error) {
	return b.JS.PullSubscribe(subject, durable, nats.ManualAck(), nats.AckExplicit(), nats.AckWait(60*time.Second), nats.MaxAckPending(1))
}
