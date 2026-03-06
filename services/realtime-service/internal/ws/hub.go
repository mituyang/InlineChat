package ws

import "sync"

// Hub 维护 conversation_id -> clients 的映射，用于会话级广播。
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[*Client]struct{})}
}

func (h *Hub) Register(conversationID string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[conversationID]; !ok {
		h.clients[conversationID] = make(map[*Client]struct{})
	}
	h.clients[conversationID][c] = struct{}{}
}

func (h *Hub) Unregister(conversationID string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	bucket, ok := h.clients[conversationID]
	if !ok {
		return
	}
	delete(bucket, c)
	if len(bucket) == 0 {
		delete(h.clients, conversationID)
	}
}

func (h *Hub) Broadcast(conversationID string, payload []byte) {
	h.mu.RLock()
	bucket := h.clients[conversationID]
	clients := make([]*Client, 0, len(bucket))
	for c := range bucket {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, client := range clients {
		if !client.TrySend(payload) {
			// 写队列满说明该连接已阻塞，主动关闭以释放资源。
			client.Close()
			continue
		}
	}
}
