package ws

import "sync"

type ClientMeta struct {
	Role string
}

// Hub 维护 conversation_id -> clients 的映射，用于会话级广播。
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]ClientMeta
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[*Client]ClientMeta)}
}

func (h *Hub) Register(conversationID string, c *Client, meta ClientMeta) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[conversationID]; !ok {
		h.clients[conversationID] = make(map[*Client]ClientMeta)
	}
	h.clients[conversationID][c] = meta
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

func (h *Hub) Broadcast(conversationID string, payload []byte, senderRole string) bool {
	h.mu.RLock()
	bucket := h.clients[conversationID]
	clients := make([]struct {
		client *Client
		meta   ClientMeta
	}, 0, len(bucket))
	for c, meta := range bucket {
		clients = append(clients, struct {
			client *Client
			meta   ClientMeta
		}{client: c, meta: meta})
	}
	h.mu.RUnlock()

	deliveredToCounterpart := false
	for _, item := range clients {
		if !item.client.TrySend(payload) {
			// 写队列满说明该连接已阻塞，主动关闭以释放资源。
			item.client.Close()
			continue
		}
		// 用“是否投递到对端角色”作为 delivered 推进的判据。
		if senderRole != "" && item.meta.Role != "" && item.meta.Role != senderRole {
			deliveredToCounterpart = true
		}
	}
	return deliveredToCounterpart
}
