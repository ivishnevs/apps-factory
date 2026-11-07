package storage

import "context"

// The person behind a request (people-attribution): Core puts the signed-in person (or the Telegram chat's) into
// the context; storage records it next to "owner" on messages, tasks, comments, answers, approvals, decisions and
// feed rows. CLI tokens and the operator carry none and stay "owner".

type personKey struct{}

// Person is who acted: a user id and the name shown.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Channel is where the person acts from: "web" or "telegram" (telegram-reply-routing).
	Channel string `json:"channel,omitempty"`
}

// WithPerson returns ctx carrying p.
func WithPerson(ctx context.Context, p Person) context.Context {
	if p.ID == "" && p.Name == "" {
		return ctx
	}
	return context.WithValue(ctx, personKey{}, p)
}

// PersonFrom is the context's person (zero when none).
func PersonFrom(ctx context.Context) Person {
	p, _ := ctx.Value(personKey{}).(Person)
	return p
}

// personName is the context's person's name, or its id when it has no name.
func personName(ctx context.Context) string {
	p := PersonFrom(ctx)
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}
