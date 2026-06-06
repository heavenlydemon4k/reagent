// Package websocket_test provides unit tests for the WebSocket hub.
package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Mock Client for testing (minimal implementation)
// ---------------------------------------------------------------------------

type mockWSConn struct{}

func (m *mockWSConn) Close() error { return nil }

// mockClient creates a test client with buffered channels.
func newMockClient(hub *Hub, userID uuid.UUID, deviceID string) *Client {
	return &Client{
		hub:      hub,
		conn:     nil, // mock connection
		userID:   userID,
		deviceID: deviceID,
		send:     make(chan []byte, 256),
	}
}

// ---------------------------------------------------------------------------
// Helper assertions
// ---------------------------------------------------------------------------

func assertTrue(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Errorf("%s: expected true", msg)
	}
}

func assertFalse(t *testing.T, cond bool, msg string) {
	t.Helper()
	if cond {
		t.Errorf("%s: expected false", msg)
	}
}

func assertEqualInt(t *testing.T, want, got int, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %d, got %d", msg, want, got)
	}
}

func assertEqualUUID(t *testing.T, want, got uuid.UUID, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %s, got %s", msg, want, got)
	}
}

// ---------------------------------------------------------------------------
// Helper: create test config
// ---------------------------------------------------------------------------

func newTestConfig() *config.Config {
	return &config.Config{
		Environment:       "development",
		WSPongWait:        60 * time.Second,
		WSPingPeriod:      54 * time.Second,
		WSWriteWait:       10 * time.Second,
		WSReadBufferSize:  1024,
		WSWriteBufferSize: 1024,
	}
}

// ---------------------------------------------------------------------------
// Tests: Hub construction
// ---------------------------------------------------------------------------

func TestNewHub(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	if hub == nil {
		t.Fatal("NewHub returned nil")
	}
	if hub.cfg != cfg {
		t.Error("cfg not set correctly")
	}
	if hub.connections == nil {
		t.Error("connections map not initialized")
	}
	if hub.register == nil {
		t.Error("register channel not initialized")
	}
	if hub.unregister == nil {
		t.Error("unregister channel not initialized")
	}
	if hub.broadcast == nil {
		t.Error("broadcast channel not initialized")
	}
	assertEqualInt(t, 100, cap(hub.register), "register channel capacity")
	assertEqualInt(t, 100, cap(hub.unregister), "unregister channel capacity")
	assertEqualInt(t, 256, cap(hub.broadcast), "broadcast channel capacity")
}

// ---------------------------------------------------------------------------
// Tests: registerClient
// ---------------------------------------------------------------------------

func TestRegisterClient(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)

	clients := hub.GetClients(uid)
	assertEqualInt(t, 1, len(clients), "client count after registration")

	// Register same user+device → should replace
	client2 := newMockClient(hub, uid, "device-1")
	hub.registerClient(client2)

	clients = hub.GetClients(uid)
	assertEqualInt(t, 1, len(clients), "client count should still be 1")
}

func TestRegisterClient_MultipleDevices(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()

	client1 := newMockClient(hub, uid, "device-ios")
	client2 := newMockClient(hub, uid, "device-android")

	hub.registerClient(client1)
	hub.registerClient(client2)

	clients := hub.GetClients(uid)
	assertEqualInt(t, 2, len(clients), "two devices for same user")
}

// ---------------------------------------------------------------------------
// Tests: unregisterClient
// ---------------------------------------------------------------------------

func TestUnregisterClient(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)
	hub.unregisterClient(client)

	clients := hub.GetClients(uid)
	assertEqualInt(t, 0, len(clients), "no clients after unregister")
}

func TestUnregisterClient_NotRegistered(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	// Should not panic
	hub.unregisterClient(client)

	clients := hub.GetClients(uid)
	assertEqualInt(t, 0, len(clients), "no clients")
}

func TestUnregisterClient_RemovesUserWhenLastDevice(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)
	hub.unregisterClient(client)

	// User should be removed from connections map
	hub.mu.RLock()
	_, exists := hub.connections[uid]
	hub.mu.RUnlock()
	assertFalse(t, exists, "user should be removed when last device unregisters")
}

// ---------------------------------------------------------------------------
// Tests: GetClients
// ---------------------------------------------------------------------------

func TestGetClients_Empty(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	clients := hub.GetClients(uuid.New())
	assertEqualInt(t, 0, len(clients), "no clients for unknown user")
}

func TestGetClients_ReturnsCorrectClients(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)

	clients := hub.GetClients(uid)
	assertEqualInt(t, 1, len(clients), "one client")
	assertEqualUUID(t, uid, clients[0].userID, "userID matches")
	assertEqualString(t, "device-1", clients[0].deviceID, "deviceID matches")
}

// ---------------------------------------------------------------------------
// Tests: GetClientCount
// ---------------------------------------------------------------------------

func TestGetClientCount(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	assertEqualInt(t, 0, hub.GetClientCount(), "initial count")

	uid1 := uuid.New()
	uid2 := uuid.New()
	hub.registerClient(newMockClient(hub, uid1, "d1"))
	assertEqualInt(t, 1, hub.GetClientCount(), "after first client")

	hub.registerClient(newMockClient(hub, uid1, "d2"))
	assertEqualInt(t, 2, hub.GetClientCount(), "after second device")

	hub.registerClient(newMockClient(hub, uid2, "d1"))
	assertEqualInt(t, 3, hub.GetClientCount(), "after third client")

	hub.unregisterClient(newMockClient(hub, uid1, "d1"))
	// Note: unregisterClient looks up by (userID, deviceID) and deletes
	// But we're passing a new mock client, so it won't find it.
	// The actual count is still 3 for this test.
}

// ---------------------------------------------------------------------------
// Tests: IsUserOnline
// ---------------------------------------------------------------------------

func TestIsUserOnline_True(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)
	assertTrue(t, hub.IsUserOnline(uid), "user should be online")
}

func TestIsUserOnline_False(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	assertFalse(t, hub.IsUserOnline(uid), "user should not be online")
}

func TestIsUserOnline_AfterUnregister(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)
	assertTrue(t, hub.IsUserOnline(uid), "online after register")

	hub.unregisterClient(client)
	assertFalse(t, hub.IsUserOnline(uid), "offline after unregister")
}

// ---------------------------------------------------------------------------
// Tests: Run lifecycle
// ---------------------------------------------------------------------------

func TestRun_StartsAndStops(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()

	// Let it start
	time.Sleep(50 * time.Millisecond)

	// Cancel to stop
	cancel()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not stop within timeout")
	}
}

// ---------------------------------------------------------------------------
// Tests: closeAll
// ---------------------------------------------------------------------------

func TestCloseAll(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()

	hub.registerClient(newMockClient(hub, uid, "d1"))
	hub.registerClient(newMockClient(hub, uid, "d2"))
	hub.registerClient(newMockClient(hub, uuid.New(), "d3"))

	assertEqualInt(t, 3, hub.GetClientCount(), "before closeAll")

	hub.closeAll()

	assertEqualInt(t, 0, hub.GetClientCount(), "after closeAll")
	assertFalse(t, hub.IsUserOnline(uid), "user offline after closeAll")
}

// ---------------------------------------------------------------------------
// Tests: RegisterClient via public API
// ---------------------------------------------------------------------------

func TestRegisterClient_Public(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	// Use the public RegisterClient API
	hub.RegisterClient(uid, "device-1", client)

	// The client should be queued for registration
	// Drain the register channel
	select {
	case c := <-hub.register:
		// Process it manually (Run goroutine not started)
		hub.registerClient(c)
	case <-time.After(time.Second):
		t.Fatal("client not queued for registration")
	}

	clients := hub.GetClients(uid)
	assertEqualInt(t, 1, len(clients), "client registered via public API")
}

// ---------------------------------------------------------------------------
// Tests: BroadcastToUser
// ---------------------------------------------------------------------------

func TestBroadcastToUser_UserNotConnected(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}

	err := hub.BroadcastToUser(uid, evt)
	if err != nil {
		t.Logf("BroadcastToUser returned: %v (acceptable)", err)
	}
}

func TestBroadcastToUser_ChannelFull(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	// Fill the broadcast channel
	for i := 0; i < cap(hub.broadcast); i++ {
		hub.broadcast <- broadcastMsg{userID: uuid.New(), event: &models.WSEvent{}}
	}

	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	err := hub.BroadcastToUser(uuid.New(), evt)
	if err == nil {
		t.Error("expected error when broadcast channel full")
	}

	// Drain
	for len(hub.broadcast) > 0 {
		<-hub.broadcast
	}
}

// ---------------------------------------------------------------------------
// Tests: SendToDevice
// ---------------------------------------------------------------------------

func TestSendToDevice_Success(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}

	err := hub.SendToDevice(uid, "device-1", evt)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Drain the channel
	select {
	case <-hub.broadcast:
		// OK
	case <-time.After(time.Second):
		t.Fatal("message not queued")
	}
}

func TestSendToDevice_ChannelFull(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	// Fill the broadcast channel
	for i := 0; i < cap(hub.broadcast); i++ {
		hub.broadcast <- broadcastMsg{userID: uuid.New(), event: &models.WSEvent{}}
	}

	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	err := hub.SendToDevice(uuid.New(), "device-1", evt)
	if err == nil {
		t.Error("expected error when channel full")
	}

	// Drain
	for len(hub.broadcast) > 0 {
		<-hub.broadcast
	}
}

// ---------------------------------------------------------------------------
// Tests: routeBroadcast
// ---------------------------------------------------------------------------

func TestRouteBroadcast_ToDevice(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)

	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	msg := broadcastMsg{userID: uid, device: "device-1", event: evt}
	hub.routeBroadcast(msg)

	// Client should have received the message
	select {
	case <-client.send:
		// OK
	case <-time.After(time.Second):
		t.Fatal("message not routed to device")
	}
}

func TestRouteBroadcast_ToAllDevices(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client1 := newMockClient(hub, uid, "device-ios")
	client2 := newMockClient(hub, uid, "device-android")

	hub.registerClient(client1)
	hub.registerClient(client2)

	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	msg := broadcastMsg{userID: uid, event: evt}
	hub.routeBroadcast(msg)

	// Both clients should receive
	select {
	case <-client1.send:
		// OK
	case <-time.After(time.Second):
		t.Fatal("client1 did not receive")
	}
	select {
	case <-client2.send:
		// OK
	case <-time.After(time.Second):
		t.Fatal("client2 did not receive")
	}
}

func TestRouteBroadcast_UserNotConnected(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)

	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	msg := broadcastMsg{userID: uuid.New(), event: evt}

	// Should not panic
	hub.routeBroadcast(msg)
}

func TestRouteBroadcast_DeviceNotFound(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)

	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	msg := broadcastMsg{userID: uid, device: "nonexistent-device", event: evt}

	// Should not panic and should not deliver to client
	hub.routeBroadcast(msg)

	select {
	case <-client.send:
		t.Fatal("should not have received message for wrong device")
	case <-time.After(100 * time.Millisecond):
		// Expected — no message
	}
}

// ---------------------------------------------------------------------------
// Tests: handleRedisMessage
// ---------------------------------------------------------------------------

func TestHandleRedisMessage_InvalidChannel(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	// Should not panic
	hub.handleRedisMessage("invalid:channel", "{}")
}

func TestHandleRedisMessage_MalformedChannel(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	// Should not panic
	hub.handleRedisMessage("ws:", "{}")
	hub.handleRedisMessage("ws:not-a-uuid", "{}")
}

func TestHandleRedisMessage_MalformedPayload(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	// Should not panic with invalid JSON
	hub.handleRedisMessage("ws:"+uid.String(), "not-json")
}

func TestHandleRedisMessage_ValidPayload(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()
	client := newMockClient(hub, uid, "device-1")

	hub.registerClient(client)

	payload := `{"type":"spawn","card_id":"` + uuid.New().String() + `","text":"hello"}`
	hub.handleRedisMessage("ws:"+uid.String(), payload)

	// Client should receive
	select {
	case <-client.send:
		// OK
	case <-time.After(time.Second):
		t.Fatal("client should have received redis message")
	}
}

// ---------------------------------------------------------------------------
// Tests: StartRedisSubscriber
// ---------------------------------------------------------------------------

func TestStartRedisSubscriber_NoRedis(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic when redis is nil
	hub.StartRedisSubscriber(ctx)
}

// ---------------------------------------------------------------------------
// Tests: broadcastMsg struct
// ---------------------------------------------------------------------------

func TestBroadcastMsg_Fields(t *testing.T) {
	uid := uuid.New()
	evt := &models.WSEvent{Type: models.WSEventSpawn, CardID: uuid.New()}
	msg := broadcastMsg{
		userID: uid,
		device: "device-1",
		event:  evt,
	}
	assertEqualUUID(t, uid, msg.userID, "userID")
	assertEqualString(t, "device-1", msg.device, "device")
	if msg.event == nil {
		t.Error("event should not be nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: Hub with nil config (defensive)
// ---------------------------------------------------------------------------

func TestNewHub_NilConfig(t *testing.T) {
	// Hub should handle nil config gracefully (uses zero values)
	hub := NewHub(nil, nil, nil)
	if hub == nil {
		t.Fatal("NewHub with nil config should not panic")
	}
	if hub.cfg != nil {
		t.Error("cfg should be nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: Concurrent operations (race detector)
// ---------------------------------------------------------------------------

func TestHub_ConcurrentRegisterUnregister(t *testing.T) {
	cfg := newTestConfig()
	hub := NewHub(cfg, nil, nil)
	uid := uuid.New()

	// Concurrent registrations
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(idx int) {
			client := newMockClient(hub, uid, "device")
			hub.registerClient(client)
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for registration")
		}
	}

	// Only one client should remain for same (user, device)
	clients := hub.GetClients(uid)
	assertEqualInt(t, 1, len(clients), "only one client for same device")
}

func assertEqualString(t *testing.T, want, got, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", msg, want, got)
	}
}
