// Package audit writes the append-only, hash-chained audit log (FR-K06, NFR-I06)
// and the event stream consumed by Activity views.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"continuum/internal/store"
)

// Entry is one audit record.
type Entry struct {
	Seq         int64  `json:"seq"`
	TS          string `json:"ts"`
	Actor       string `json:"actor"`
	Action      string `json:"action"`
	Target      string `json:"target,omitempty"`
	NodeID      string `json:"node_id,omitempty"`
	VolumeID    string `json:"volume_id,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Checksum    string `json:"checksum,omitempty"`
	Result      string `json:"result"`
	PrevHash    string `json:"prev_hash"`
	Hash        string `json:"hash"`
}

// Log appends audit entries. Appends are serialised so the chain stays linear.
type Log struct {
	db *store.DB
	mu sync.Mutex
}

func New(db *store.DB) *Log { return &Log{db: db} }

const genesis = "0000000000000000000000000000000000000000000000000000000000000000"

func (e *Entry) digest() string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		e.Seq, e.TS, e.Actor, e.Action, e.Target, e.NodeID, e.VolumeID,
		e.Source, e.Destination, e.Checksum, e.Result, e.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Append writes e, filling Seq/TS/PrevHash/Hash.
func (l *Log) Append(ctx context.Context, e Entry) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var last sql.NullInt64
	var prev sql.NullString
	err := l.db.QueryRow(ctx, `SELECT seq, hash FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&last, &prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return e, err
	}
	e.Seq = last.Int64 + 1
	e.PrevHash = genesis
	if prev.Valid {
		e.PrevHash = prev.String
	}
	if e.TS == "" {
		e.TS = store.Now()
	}
	if e.Actor == "" {
		e.Actor = ActorFrom(ctx)
	}
	e.Hash = e.digest()
	_, err = l.db.Exec(ctx, `INSERT INTO audit (seq, ts, actor, action, target, node_id, volume_id, source, destination, checksum, result, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.Seq, e.TS, e.Actor, e.Action, e.Target, e.NodeID, e.VolumeID, e.Source, e.Destination, e.Checksum, e.Result, e.PrevHash, e.Hash)
	return e, err
}

// List returns entries newest first.
func (l *Log) List(ctx context.Context, limit int) ([]Entry, error) {
	rows, err := l.db.Query(ctx, `SELECT seq, ts, actor, action, COALESCE(target,''), COALESCE(node_id,''), COALESCE(volume_id,''),
		COALESCE(source,''), COALESCE(destination,''), COALESCE(checksum,''), result, prev_hash, hash
		FROM audit ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Seq, &e.TS, &e.Actor, &e.Action, &e.Target, &e.NodeID, &e.VolumeID,
			&e.Source, &e.Destination, &e.Checksum, &e.Result, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Verify walks the chain; returns the first broken seq (0 = intact).
func (l *Log) Verify(ctx context.Context) (int64, error) {
	rows, err := l.db.Query(ctx, `SELECT seq, ts, actor, action, COALESCE(target,''), COALESCE(node_id,''), COALESCE(volume_id,''),
		COALESCE(source,''), COALESCE(destination,''), COALESCE(checksum,''), result, prev_hash, hash
		FROM audit ORDER BY seq ASC`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prev := genesis
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Seq, &e.TS, &e.Actor, &e.Action, &e.Target, &e.NodeID, &e.VolumeID,
			&e.Source, &e.Destination, &e.Checksum, &e.Result, &e.PrevHash, &e.Hash); err != nil {
			return 0, err
		}
		if e.PrevHash != prev || e.digest() != e.Hash {
			return e.Seq, nil
		}
		prev = e.Hash
	}
	return 0, rows.Err()
}

// Event is one Activity timeline item.
type Event struct {
	ID       string          `json:"event_id"`
	TS       string          `json:"ts"`
	Kind     string          `json:"kind"`
	NodeID   string          `json:"node_id,omitempty"`
	VolumeID string          `json:"volume_id,omitempty"`
	DiskID   string          `json:"disk_id,omitempty"`
	JobID    string          `json:"job_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Emit stores an event; payload is marshalled as JSON.
func (l *Log) Emit(ctx context.Context, ev Event, payload any) error {
	ev.ID = store.NewID("evt")
	ev.TS = store.Now()
	var p []byte
	if payload != nil {
		p, _ = json.Marshal(payload)
	}
	_, err := l.db.Exec(ctx, `INSERT INTO events (event_id, ts, kind, node_id, volume_id, disk_id, job_id, payload_json) VALUES (?,?,?,?,?,?,?,?)`,
		ev.ID, ev.TS, ev.Kind, nullIf(ev.NodeID), nullIf(ev.VolumeID), nullIf(ev.DiskID), nullIf(ev.JobID), nullIf(string(p)))
	return err
}

// Events lists newest first, optionally filtered by kind prefix.
func (l *Log) Events(ctx context.Context, kindPrefix string, limit int) ([]Event, error) {
	rows, err := l.db.Query(ctx, `SELECT event_id, ts, kind, COALESCE(node_id,''), COALESCE(volume_id,''), COALESCE(disk_id,''), COALESCE(job_id,''), COALESCE(payload_json,'')
		FROM events WHERE kind LIKE ? ORDER BY ts DESC LIMIT ?`, kindPrefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var ev Event
		var p string
		if err := rows.Scan(&ev.ID, &ev.TS, &ev.Kind, &ev.NodeID, &ev.VolumeID, &ev.DiskID, &ev.JobID, &p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p) != "" {
			ev.Payload = json.RawMessage(p)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type actorKey struct{}

// WithActor tags ctx with the authenticated user so every audit entry made under it names them (FR-S01).
func WithActor(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, actorKey{}, user)
}

// ActorFrom returns the user of ctx ("local" in single-user mode).
func ActorFrom(ctx context.Context) string {
	if u, ok := ctx.Value(actorKey{}).(string); ok && u != "" {
		return u
	}
	return "local"
}
