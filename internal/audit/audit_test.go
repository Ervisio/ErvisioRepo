package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

type fakeInserter struct {
	got []dbgen.InsertAuditEventParams
	err error
}

func (f *fakeInserter) InsertAuditEvent(_ context.Context, arg dbgen.InsertAuditEventParams) error {
	f.got = append(f.got, arg)
	return f.err
}

func TestRecord(t *testing.T) {
	f := &fakeInserter{}
	actor := uuid.New()
	if err := Record(context.Background(), f, Event{Actor: actor, Action: ActionLogin, TargetType: "user",
		TargetID: actor.String(), IP: "203.0.113.7", Data: map[string]any{"method": "password"}}); err != nil {
		t.Fatal(err)
	}
	if err := Record(context.Background(), f, Event{Action: ActionLoginFailed}); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 2 {
		t.Fatalf("got %d events", len(f.got))
	}
	a, b := f.got[0], f.got[1]
	if !a.ActorID.Valid || a.ActorID.UUID != actor || a.Ip != "203.0.113.7" || a.ID.Version() != 7 {
		t.Fatalf("bad event %+v", a)
	}
	var data map[string]string
	if err := json.Unmarshal(a.Data, &data); err != nil || data["method"] != "password" {
		t.Fatalf("data %s: %v", a.Data, err)
	}
	if b.ActorID.Valid || string(b.Data) != "{}" {
		t.Fatalf("anonymous event %+v", b)
	}
	f.err = errors.New("db down")
	if err := Record(context.Background(), f, Event{Action: ActionLogout}); !errors.Is(err, f.err) {
		t.Fatalf("error not wrapped: %v", err)
	}
}
