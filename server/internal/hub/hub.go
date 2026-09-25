// Package hub fans out "vault changed" notifications to connected devices.
package hub

import "sync"

type Client struct {
	VaultID  int64
	DeviceID int64
	C        chan int64 // latest revision; buffered, coalescing
}

type Hub struct {
	mu      sync.Mutex
	clients map[int64]map[*Client]struct{}
}

func New() *Hub { return &Hub{clients: map[int64]map[*Client]struct{}{}} }

func (h *Hub) Subscribe(vaultID, deviceID int64) *Client {
	c := &Client{VaultID: vaultID, DeviceID: deviceID, C: make(chan int64, 1)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[vaultID] == nil {
		h.clients[vaultID] = map[*Client]struct{}{}
	}
	h.clients[vaultID][c] = struct{}{}
	return c
}

func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[c.VaultID], c)
	if len(h.clients[c.VaultID]) == 0 {
		delete(h.clients, c.VaultID)
	}
}

// Notify tells every subscriber of vaultID that the vault is now at rev.
// Slow clients never block: a pending older value is replaced.
func (h *Hub) Notify(vaultID, rev int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[vaultID] {
		select {
		case c.C <- rev:
		default:
			select {
			case <-c.C:
			default:
			}
			select {
			case c.C <- rev:
			default:
			}
		}
	}
}

// OnlineDevices returns the set of device IDs with an open connection.
func (h *Hub) OnlineDevices() map[int64]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[int64]bool{}
	for _, cs := range h.clients {
		for c := range cs {
			out[c.DeviceID] = true
		}
	}
	return out
}
