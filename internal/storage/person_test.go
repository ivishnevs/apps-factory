package storage

import (
	"context"
	"testing"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// TestPeopleAttribution (people-attribution 2.1): what a person does is recorded with them next to "owner".
func TestPeopleAttribution(t *testing.T) {
	s := engineStore(t)
	co := DefaultCompany
	ctx := WithPerson(context.Background(), Person{ID: "u-mia", Name: "Mia"})
	v, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "p", Title: "t", Assignee: "demo", RequestID: "t"})
	if err != nil || v.CreatedBy != "owner" || v.CreatedByPerson != "Mia" {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err = s.CommentTask(ctx, co, "t", tasks.Comment{Text: "please hurry", RequestID: "c"}); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Comments(ctx, co, "t")
	if len(cs) == 0 || cs[len(cs)-1].Person != "Mia" {
		t.Fatalf("%+v", cs)
	}
	m, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", Sender: "owner", Recipient: "demo", MessageType: "notification", Body: "hi", RequestID: "m"})
	if err != nil || m.Person != "Mia" || m.PersonID != "u-mia" {
		t.Fatalf("%+v %v", m, err)
	}
	page, _ := s.Feed(context.Background(), co, FeedFilter{Task: "t"}, 0, 50)
	found := false
	for _, it := range page.Items {
		found = found || it.Person == "Mia"
	}
	if !found {
		t.Fatalf("feed without the person: %+v", page.Items)
	}
	// Without a person (the CLI, agents) nothing changes.
	w, _ := s.CreateTask(context.Background(), co, tasks.Create{ID: "u", ProjectID: "p", Title: "u", RequestID: "u"})
	if w.CreatedByPerson != "" {
		t.Fatal(w.CreatedByPerson)
	}
}
