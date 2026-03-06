package ws

import "testing"

func TestHubBroadcastSendsToAllClients(t *testing.T) {
	hub := NewHub()
	visitor := &Client{send: make(chan []byte, 1)}
	agent := &Client{send: make(chan []byte, 1)}

	hub.Register("1", visitor)
	hub.Register("1", agent)

	hub.Broadcast("1", []byte("x"))

	select {
	case <-visitor.send:
	default:
		t.Fatal("expected visitor to receive broadcast")
	}
	select {
	case <-agent.send:
	default:
		t.Fatal("expected agent to receive broadcast")
	}
}

func TestHubBroadcastSkipsFullClientQueue(t *testing.T) {
	hub := NewHub()
	client := &Client{send: make(chan []byte, 1)}
	client.send <- []byte("busy")

	hub.Register("1", client)

	hub.Broadcast("1", []byte("x"))

	if got, ok := <-client.send; !ok || string(got) != "busy" {
		t.Fatal("expected buffered message to remain readable before close")
	}
	if _, ok := <-client.send; ok {
		t.Fatal("expected full client queue to be closed after draining buffer")
	}
}
