// Package websocket provides a WebSocket hub for real-time client communication.
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/decisionstack/sync/internal/auth"
	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/decisionstack/sync/internal/redis"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ============================================================================
// Hub — WebSocket connection manager
// ============================================================================

// Hub manages WebSocket client registrations, unregistrations, and event
// distribution. It runs a central goroutine that serializes access to the
// connections map to prevent data races.
//
// Events are distributed both locally (same process) and via Redis pub/sub
// for cross-instance broadcasting in multi-node deployments.
type Hub struct {
	cfg *config.Config

	// connections maps userID -> deviceID -> *Client
	// Guarded by mu for direct access; the Run goroutine uses select channels.
	connections map[uuid.UUID]map[string]*Client
	mu          sync.RWMutex

	// channels for the Run goroutine
	register   chan *Client
	unregister chan *Client
	broadcast  chan broadcastMsg

	// pongWait is the maximum time to wait for a pong response
	pongWait time.Duration

	// Redis for cross-instance event distribution and session tracking
	redis *redis.Redis

	// tokenValidator validates JWT tokens presented on WebSocket upgrade.
	tokenValidator *auth.TokenValidator

	// upgrader handles HTTP -> WebSocket upgrades.
	upgrader websocket.Upgrader
}

// broadcastMsg is an internal envelope for hub-wide broadcasts.
type broadcastMsg struct {
	userID uuid.UUID      // target user; zero UUID = all users
	device string         // target device; empty = all devices for user
	event  *models.WSEvent // the event to deliver
}

// NewHub creates a new WebSocket hub with the given configuration.
func NewHub(cfg *config.Config, redisClient *redis.Redis, tokenValidator *auth.TokenValidator) *Hub {
	var upgrader websocket.Upgrader
	var pongWait time.Duration
	if cfg != nil {
		upgrader = websocket.Upgrader{
			ReadBufferSize:  cfg.WSReadBufferSize,
			WriteBufferSize: cfg.WSWriteBufferSize,
			CheckOrigin: func(r *http.Request) bool {
				if cfg.IsDevelopment() {
					return true
				}
				origin := r.Header.Get("Origin")
				allowedOrigins := cfg.AllowedWSOrigins()
				for _, allowed := range allowedOrigins {
					if origin == allowed {
						return true
					}
				}
				return false
			},
		}
		pongWait = cfg.WSPongWait
	}

	return &Hub{
		cfg:            cfg,
		connections:    make(map[uuid.UUID]map[string]*Client),
		register:       make(chan *Client, 100),
		unregister:     make(chan *Client, 100),
		broadcast:      make(chan broadcastMsg, 256),
		pongWait:       pongWait,
		redis:          redisClient,
		tokenValidator: tokenValidator,
		upgrader:       upgrader,
	}
}

// ============================================================================
// LIFECYCLE
// ============================================================================

// Run starts the hub's central goroutine that handles register, unregister,
// and broadcast messages. This method blocks until the context is cancelled.
func (h *Hub) Run(ctx context.Context) {
	logger.Info("websocket hub started")

	for {
		select {
		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case msg := <-h.broadcast:
			h.routeBroadcast(msg)

		case <-ctx.Done():
			logger.Info("websocket hub shutting down")
			h.closeAll()
			return
		}
	}
}

// ============================================================================
// REGISTRATION
// ============================================================================

// RegisterClient queues a client for registration on the hub's goroutine.
// If a client already exists for the same (userID, deviceID) pair, the old
// client is closed (single-device-per-connection policy).
func (h *Hub) RegisterClient(userID uuid.UUID, deviceID string, client *Client) {
	h.register <- client
}

// registerClient performs the actual registration (called from Run goroutine).
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.connections[client.userID] == nil {
		h.connections[client.userID] = make(map[string]*Client)
	}

	// Disconnect existing client for this device (single connection per device)
	if existing, ok := h.connections[client.userID][client.deviceID]; ok {
		logger.Debug("disconnecting existing client for device",
			"user_id", client.userID,
			"device_id", client.deviceID,
		)
		close(existing.send)
		if existing.conn != nil {
			existing.conn.Close()
		}
	}

	h.connections[client.userID][client.deviceID] = client
	logger.Debug("websocket client registered",
		"user_id", client.userID,
		"device_id", client.deviceID,
	)
}

// unregisterClient removes a client and closes its send channel.
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if devices, ok := h.connections[client.userID]; ok {
		if _, exists := devices[client.deviceID]; exists {
			delete(devices, client.deviceID)
			close(client.send)
			logger.Debug("websocket client unregistered",
				"user_id", client.userID,
				"device_id", client.deviceID,
			)

			if len(devices) == 0 {
				delete(h.connections, client.userID)
			}
		}
	}

	if client.conn != nil {
		client.conn.Close()
	}
}

// findClient returns an existing client for the given (userID, deviceID) pair,
// or nil if no match is found.
func (h *Hub) findClient(userID uuid.UUID, deviceID string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if devices, ok := h.connections[userID]; ok {
		if client, ok := devices[deviceID]; ok {
			return client
		}
	}
	return nil
}

// ============================================================================
// BROADCAST ROUTING
// ============================================================================

// BroadcastToUser sends an event to all connected devices of a user.
// The event is delivered locally and published to Redis for cross-instance
// distribution.
func (h *Hub) BroadcastToUser(userID uuid.UUID, event *models.WSEvent) error {
	msg := broadcastMsg{
		userID: userID,
		event:  event,
	}

	// Send through the hub's broadcast channel for thread-safe delivery
	select {
	case h.broadcast <- msg:
	default:
		return fmt.Errorf("hub broadcast channel full")
	}

	// Also publish to Redis for cross-instance distribution
	if h.redis != nil {
		data, err := json.Marshal(event)
		if err != nil {
			logger.Warn("failed to marshal event for redis", "error", err)
		} else {
			if err := h.redis.WSPublish(context.Background(), userID, data); err != nil {
				logger.Warn("failed to publish ws event to redis", "user_id", userID, "error", err)
			}
		}
	}

	return nil
}

// SendToDevice sends an event to a specific device of a user.
func (h *Hub) SendToDevice(userID uuid.UUID, deviceID string, event *models.WSEvent) error {
	msg := broadcastMsg{
		userID: userID,
		device: deviceID,
		event:  event,
	}

	select {
	case h.broadcast <- msg:
		return nil
	default:
		return fmt.Errorf("hub broadcast channel full")
	}
}

// routeBroadcast routes a broadcast message to the appropriate clients.
func (h *Hub) routeBroadcast(msg broadcastMsg) {
	data, err := json.Marshal(msg.event)
	if err != nil {
		logger.Error("failed to marshal broadcast event", "error", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	// If device is specified, send only to that device
	if msg.device != "" {
		if devices, ok := h.connections[msg.userID]; ok {
			if client, ok := devices[msg.device]; ok {
				select {
				case client.send <- data:
				default:
					h.unregister <- client
				}
			}
		}
		return
	}

	// Send to all devices for the user
	if devices, ok := h.connections[msg.userID]; ok {
		for _, client := range devices {
			select {
			case client.send <- data:
			default:
				// Client send buffer full, trigger unregister
				go func(c *Client) {
					h.unregister <- c
				}(client)
			}
		}
	}
}

// ============================================================================
// REDIS SUBSCRIBER — cross-instance event distribution
// ============================================================================

// StartRedisSubscriber starts a goroutine that subscribes to Redis pub/sub
// channels for each user that has connected clients. This enables cross-node
// WebSocket event distribution.
func (h *Hub) StartRedisSubscriber(ctx context.Context) {
	if h.redis == nil {
		logger.Info("redis not configured, skipping cross-instance pub/sub")
		return
	}

	// Subscribe to a global channel for all users
	// Individual user channels are subscribed on-demand
	go h.redisSubscriberLoop(ctx)
}

func (h *Hub) redisSubscriberLoop(ctx context.Context) {
	// Pattern subscribe to ws:* channels
	pubsub := h.redis.Client().PSubscribe(ctx, "ws:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				logger.Warn("redis pub/sub channel closed")
				return
			}
			h.handleRedisMessage(msg.Channel, msg.Payload)

		case <-ctx.Done():
			logger.Info("redis subscriber shutting down")
			return
		}
	}
}

func (h *Hub) handleRedisMessage(channel string, payload string) {
	// Channel format: ws:{userID}
	// Extract userID from channel name
	var userID uuid.UUID
	if _, err := fmt.Sscanf(channel, "ws:%s", &userID); err != nil {
		// Try parsing the suffix
		prefix := "ws:"
		if len(channel) > len(prefix) {
			id, err := uuid.Parse(channel[len(prefix):])
			if err != nil {
				logger.Warn("invalid redis channel format", "channel", channel)
				return
			}
			userID = id
		}
	}

	if userID == uuid.Nil {
		logger.Warn("could not extract userID from redis channel", "channel", channel)
		return
	}

	var event models.WSEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		logger.Warn("failed to unmarshal redis message", "error", err)
		return
	}

	// Deliver to local clients for this user (do NOT re-publish to Redis)
	// Collect matching clients under lock, then send outside the lock to
	// avoid blocking the read lock on a full client send buffer.
	h.mu.RLock()
	var clients []*Client
	if devices, ok := h.connections[userID]; ok {
		for _, client := range devices {
			clients = append(clients, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range clients {
		select {
		case client.send <- []byte(payload):
		default:
			// Buffer full — unregister asynchronously
			go func(c *Client) { h.unregister <- c }(client)
		}
	}
}

// ============================================================================
// QUERIES
// ============================================================================

// GetClients returns all connected clients for a user.
func (h *Hub) GetClients(userID uuid.UUID) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var clients []*Client
	if devices, ok := h.connections[userID]; ok {
		for _, c := range devices {
			clients = append(clients, c)
		}
	}
	return clients
}

// GetClientCount returns the total number of connected clients.
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	count := 0
	for _, devices := range h.connections {
		count += len(devices)
	}
	return count
}

// IsUserOnline returns true if the user has at least one connected device.
func (h *Hub) IsUserOnline(userID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	devices, ok := h.connections[userID]
	return ok && len(devices) > 0
}

// ============================================================================
// SHUTDOWN
// ============================================================================

// closeAll closes all active connections.
func (h *Hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for userID, devices := range h.connections {
		for deviceID, client := range devices {
			close(client.send)
			if client.conn != nil {
				client.conn.Close()
			}
			delete(devices, deviceID)
		}
		delete(h.connections, userID)
	}
}
