package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeMessaging(t *testing.T) {
	var in bytes.Buffer
	WriteNative(&in, map[string]string{"type": "hello"})
	WriteNative(&in, map[string]string{"type": "discover"})
	WriteNative(&in, map[string]string{"type": "nope"})
	var out bytes.Buffer
	if err := ServeNative(context.Background(), t.TempDir(), &in, &out); err != nil {
		t.Fatal(err)
	}
	var hello, disks, bad map[string]any
	for _, v := range []*map[string]any{&hello, &disks, &bad} {
		if err := ReadNative(&out, v); err != nil {
			t.Fatal(err)
		}
	}
	if hello["type"] != "hello" || hello["enrolled"] != false || hello["controller_api"] == "" {
		t.Fatalf("hello: %v", hello)
	}
	if disks["type"] != "disks" {
		t.Fatalf("discover: %v", disks)
	}
	if bad["type"] != "error" {
		t.Fatalf("unknown: %v", bad)
	}
	var m map[string]any
	json.Unmarshal(NativeManifest("/opt/c/continuum-agent", []string{"abcdefghijklmnopabcdefghijklmnop"}), &m)
	if m["name"] != NativeHostName || m["type"] != "stdio" || !strings.Contains(m["allowed_origins"].([]any)[0].(string), "chrome-extension://abcdefghijklmnop") {
		t.Fatalf("manifest: %v", m)
	}
}
