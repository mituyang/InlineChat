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

func TestParseMessageNewEvent(t *testing.T) {
	conversationID, messageID, senderType, ok := parseMessageNewEvent([]byte(`{"type":"message.new","payload":{"conversation_id":7,"message":{"id":9,"sender_type":"visitor"}}}`))
	if !ok {
		t.Fatal("expected parseMessageNewEvent ok=true")
	}
	if conversationID != 7 || messageID != 9 || senderType != "visitor" {
		t.Fatalf("unexpected parse result: conversation_id=%d message_id=%d sender_type=%s", conversationID, messageID, senderType)
	}
}
