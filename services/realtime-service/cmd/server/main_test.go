package main

import "testing"

func TestParseMessageEventType(t *testing.T) {
	eventType, ok := parseMessageEventType([]byte(`{"type":"message.status","payload":{"conversation_id":1}}`))
	if !ok {
		t.Fatal("expected parseMessageEventType ok=true")
	}
	if eventType != "message.status" {
		t.Fatalf("unexpected event type: %s", eventType)
	}
}
