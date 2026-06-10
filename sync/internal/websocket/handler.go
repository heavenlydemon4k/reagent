// Package websocket provides a WebSocket hub for real-time client communication.
package websocket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/decisionstack/sync/internal/auth"
	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ============================================================================
// WSHandler — HTTP handler for WebSocket upgrades
// ============================================================================

// WSHandler upgrades HTTP requests to WebSocket connections and manages
// client lifecycle (read/write pumps, session binding).
type WSHandler struct {
	hub         *Hub
	tokenMgr    *auth.TokenManager
	cfg         *config.Config
	spawnEngine SpawnEngine
	sessionStore SessionStore
}

// NewWSHandler creates a new WebSocket handler.
func NewWSHandler(hub *Hub, tokenMgr *auth.TokenManager, cfg *config.Config, engine SpawnEngine, store SessionStore) *WSHandler {
	return &WSHandler{
		hub:          hub,
		tokenMgr:     tokenMgr,
		cfg:          cfg,
		spawnEngine:  engine,
		sessionStore: store,
	}
}

// Routes registers WebSocket routes on the provided Chi router.
// The route is mounted under /ws and requires ?token= JWT query parameter.
func (h *WSHandler) Routes(r chi.Router) {
	r.Get("/ws", h.ServeHTTP)
}

// ServeWS handles WebSocket upgrade with JWT authentication.
// It validates the token, checks the device ID, registers a Redis session,
// upgrades the connection, and returns the created Client.  The caller must
// start writePump / readPump after any additional client setup.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) *Client {
	// 1. Extract JWT from query parameter: ?token={JWT}
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		logger.Warn("websocket: connection rejected", "reason", "missing_token", "ip", r.RemoteAddr)
		writeWSError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "missing token", false)
		return nil
	}

	// 2. Validate JWT
	if h.tokenValidator == nil {
		logger.Warn("websocket: connection rejected", "reason", "validator_not_configured", "ip", r.RemoteAddr)
		writeWSError(w, http.StatusInternalServerError, "internal_error", "token validator not configured", false)
		return nil
	}

	claims, err := h.tokenValidator.Validate(tokenStr)
	if err != nil {
		logger.Warn("websocket: connection rejected", "reason", "invalid_token", "error", err, "ip", r.RemoteAddr)
		writeWSError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "invalid token", false)
		return nil
	}

	// 3. Check X-Device-ID header
	deviceID := r.Header.Get("X-Device-ID")
	if deviceID == "" {
		logger.Warn("websocket: connection rejected", "reason", "missing_device_id", "ip", r.RemoteAddr)
		writeWSError(w, http.StatusBadRequest, "invalid_request", "missing X-Device-ID", false)
		return nil
	}

	userIDStr := claims.UserID
	if userIDStr == "" {
		userIDStr = claims.Subject
	}
	if userIDStr == "" {
		logger.Warn("websocket: connection rejected", "reason", "missing_user_id", "ip", r.RemoteAddr)
		writeWSError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "missing user_id in token", false)
		return nil
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		logger.Warn("websocket: connection rejected", "reason", "invalid_user_id", "error", err, "ip", r.RemoteAddr)
		writeWSError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "invalid user_id in token", false)
		return nil
	}

	// 4. Check for existing connection from same device — disconnect old
	if oldClient := h.findClient(userID, deviceID); oldClient != nil {
		logger.Info("websocket: disconnecting old connection",
			"user_id", userIDStr, "device_id", deviceID)
		select {
		case h.unregister <- oldClient:
		default:
		}
	}

	// 5. Register session in Redis (4h TTL)
	if h.redis != nil {
		sessionKey := fmt.Sprintf("session:ws:%s:%s", userIDStr, deviceID)
		if err := h.redis.Client().Set(r.Context(), sessionKey, "active", 4*time.Hour).Err(); err != nil {
			logger.Warn("websocket: failed to register session", "error", err)
			// Non-fatal: continue without session tracking
		}
	}

	// 6. Upgrade to WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("websocket: upgrade failed", "error", err)
		return nil
	}

	// 7. Create client with authenticated userID
	client := &Client{
		hub:      h,
		conn:     conn,
		send:     make(chan []byte, 256),
		userID:   userID,
		deviceID: deviceID,
	}

	h.register <- client

	logger.Info("websocket: connection accepted",
		"user_id", userIDStr, "device_id", deviceID, "ip", r.RemoteAddr)

	return client
}

// ServeHTTP upgrades an HTTP connection to WebSocket.
//
// Authentication: JWT access token is extracted from ?token= query parameter.
// The token is validated and the userID/deviceID are extracted from claims.
// Delegates the core upgrade logic to Hub.ServeWS, then attaches the
// WSHandler reference and starts the read/write pumps.
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	client := h.hub.ServeWS(w, r)
	if client == nil {
		// ServeWS already wrote an error response
		return
	}

	// Attach handler reference so the client can access spawnEngine/sessionStore
	client.handler = h

	// Start read/write pumps
	go client.writePump(h.cfg)
	go client.readPump()

	logger.Info("websocket: client connected",
		"user_id", client.userID,
		"device_id", client.deviceID,
		"ip", r.RemoteAddr)
}

// ============================================================================
// Client — per-connection state
// ============================================================================

// Client represents a single WebSocket connection.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	userID   uuid.UUID
	deviceID string
	send     chan []byte

	handler *WSHandler

	// sessions maps card_id -> *SendingSession for active drafting sessions
	sessions map[uuid.UUID]*SendingSession
	mu       sync.Mutex
}

// ============================================================================
// READ PUMP — handles incoming client messages
// ============================================================================

// readPump reads messages from the WebSocket connection, parses them as
// ClientEvents, and routes them to the appropriate SendingSession handler.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(c.hub.pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(c.hub.pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Warn("websocket unexpected close",
					"error", err,
					"user_id", c.userID,
					"device_id", c.deviceID,
				)
			}
			break
		}

		c.handleMessage(message)
	}
}

// handleMessage processes a single incoming WebSocket message.
func (c *Client) handleMessage(data []byte) {
	// Parse as client event
	event, err := UnmarshalClientEvent(data)
	if err != nil {
		logger.Debug("websocket received non-event message",
			"data", "[REDACTED:non-event-data]",
			"user_id", c.userID,
		)
		return
	}

	// Validate event type
	if !IsClientEventType(event.Type) {
		logger.Warn("websocket received unsupported event type",
			"type", event.Type,
			"user_id", c.userID,
		)
		c.sendError(event.CardID, "invalid_event_type", fmt.Sprintf("event type %s not supported", event.Type), false)
		return
	}

	// Handle ping events immediately
	if event.Type == models.WSEventPing {
		pong := NewPongEvent(event.CardID)
		pongData, err := MarshalServerEvent(pong)
		if err == nil {
			select {
			case c.send <- pongData:
			default:
			}
		}
		return
	}

	// Validate card_id for events that require it
	if RequiresCardID(event.Type) && event.CardID == uuid.Nil {
		c.sendError(uuid.Nil, "missing_card_id", "card_id is required for this event type", false)
		return
	}

	logger.Debug("websocket event received",
		"type", event.Type,
		"user_id", c.userID,
		"card_id", event.CardID,
	)

	// Route to sending session
	session := c.getOrCreateSession(event.CardID)
	if err := session.HandleEvent(event); err != nil {
		logger.Error("session event handler error",
			"type", event.Type,
			"card_id", event.CardID,
			"error", err,
		)
		c.sendError(event.CardID, "handler_error", err.Error(), true)
	}
}

// getOrCreateSession returns the SendingSession for a card, creating one if needed.
func (c *Client) getOrCreateSession(cardID uuid.UUID) *SendingSession {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sessions == nil {
		c.sessions = make(map[uuid.UUID]*SendingSession)
	}

	if session, ok := c.sessions[cardID]; ok {
		return session
	}

	session := NewSendingSession(cardID, c.userID, c.deviceID, c.hub, c.handler.spawnEngine, c.handler.sessionStore)
	c.sessions[cardID] = session
	return session
}

// sendError sends an error event to this client.
func (c *Client) sendError(cardID uuid.UUID, code, message string, retry bool) {
	evt := NewWSErrorEvent(cardID, code, message, retry)
	data, err := MarshalServerEvent(evt)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

// ============================================================================
// WRITE PUMP — writes outbound messages with ping/pong
// ============================================================================

// writePump writes messages from the send channel to the WebSocket connection.
// It sends ping messages at the configured interval to keep the connection alive.
func (c *Client) writePump(cfg *config.Config) {
	ticker := time.NewTicker(cfg.WSPingPeriod)
	defer func() {
		ticker.Stop()
		// Unregister to prevent client leak if writePump exits first
		select {
		case c.hub.unregister <- c:
		default:
		}
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(cfg.WSWriteWait))
			if !ok {
				// Hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				logger.Warn("websocket write error",
					"error", err,
					"user_id", c.userID,
					"device_id", c.deviceID,
				)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(cfg.WSWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				logger.Debug("websocket ping failed, closing connection",
					"error", err,
					"user_id", c.userID,
				)
				return
			}
		}
	}
}

// ============================================================================
// HELPERS
// ============================================================================

// writeWSError writes an HTTP error response in JSON format.
func writeWSError(w http.ResponseWriter, status int, code, message string, retry bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	err := models.SyncError{Code: code, Message: message, Retry: retry}
	//nolint:errcheck // best-effort error response
	json.NewEncoder(w).Encode(err)
}
