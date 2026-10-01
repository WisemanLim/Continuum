package audit

import (
	"context"
	"testing"

	"continuum/internal/store"
	"continuum/internal/store/storetest"
)

func TestHashChain(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(storetest.URL(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	l := New(db)
	for _, a := range []string{"volume.create", "disk.attach", "copy"} {
		if _, err := l.Append(ctx, Entry{Action: a, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	if bad, err := l.Verify(ctx); err != nil || bad != 0 {
		t.Fatalf("chain should be intact: bad=%d err=%v", bad, err)
	}
	// tamper
	if _, err := db.Exec(ctx, `UPDATE audit SET result = 'forged' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	if bad, _ := l.Verify(ctx); bad != 2 {
		t.Fatalf("tamper not detected at seq 2, got %d", bad)
	}
}
