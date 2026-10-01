// Package agentproto defines the Controller↔Agent protocol (FR-N02): connect-go RPCs with a JSON codec over
// plain Go structs (no protoc step). Control runs on one bidirectional stream; file bytes move over separate
// mTLS HTTP streams (blobs) so large transfers never block heartbeats.
package agentproto

import "encoding/json"

// Codec marshals messages as JSON (content type application/connect+json).
type Codec struct{}

func (Codec) Name() string                    { return "json" }
func (Codec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (Codec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
