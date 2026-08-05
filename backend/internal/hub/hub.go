package hub

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
	up      websocket.Upgrader
}

func New() *Hub {
	return &Hub{
		clients: map[*websocket.Conn]bool{},
		up: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

type event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (h *Hub) Publish(eventType string, data any) {
	b, _ := json.Marshal(event{Type: eventType, Data: data})
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if err := c.WriteMessage(websocket.TextMessage, b); err != nil {
			c.Close()
			delete(h.clients, c)
		}
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()
	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for range ping.C {
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				break
			}
		}
	}()
	// 读循环，客户端断开时清理
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			h.mu.Lock()
			delete(h.clients, conn)
			h.mu.Unlock()
			conn.Close()
			return
		}
	}
}
