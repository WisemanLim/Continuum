package store

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

// NewID returns "<prefix>_<ULID>" (e.g. vol_01J…, dsk_01J…, cpy_01J…).
func NewID(prefix string) string {
	id := ulid.Make() // monotonic within the process: ids sort in creation order even inside one millisecond
	return prefix + "_" + strings.ToLower(id.String())
}
