package ws

import "testing"

func TestHubBroadcastDeliveredToCounterpart(t *testing.T) {
	hub := NewHub()
	visitor := &Client{send: make(chan []byte, 1)}
	agent := &Client{send: make(chan []byte, 1)}

	hub.Register("1", visitor, ClientMeta{Role: "visitor"})
	hub.Register("1", agent, ClientMeta{Role: "agent"})

	delivered := hub.Broadcast("1", []byte("x"), "visitor")
	if !delivered {
		t.Fatal("expected delivered to counterpart=true")
	}
}

func TestHubBroadcastNoCounterpart(t *testing.T) {
	hub := NewHub()
	visitorA := &Client{send: make(chan []byte, 1)}
	visitorB := &Client{send: make(chan []byte, 1)}

	hub.Register("1", visitorA, ClientMeta{Role: "visitor"})
	hub.Register("1", visitorB, ClientMeta{Role: "visitor"})

	delivered := hub.Broadcast("1", []byte("x"), "visitor")
	if delivered {
		t.Fatal("expected delivered to counterpart=false")
	}
}
