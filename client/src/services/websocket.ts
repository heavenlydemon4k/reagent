// Decision Stack — WebSocket Client for Sending Sessions
// Real-time collaboration during draft review and voice mode

import { useAuthStore } from '@stores/authStore';
import { useSyncStore } from '@stores/syncStore';

const WS_BASE_URL =
  process.env.EXPO_PUBLIC_WS_URL ?? 'wss://ws.decisionstack.app/v1';

export type WebSocketStatus = 'connecting' | 'open' | 'closing' | 'closed' | 'error';

export type WSEventType =
  | 'session.joined'
  | 'session.left'
  | 'draft.updated'
  | 'draft.approved'
  | 'draft.sent'
  | 'card.updated'
  | 'voice.transcription'
  | 'voice.state_change'
  | 'error';

export interface WSEvent {
  type: WSEventType;
  payload: unknown;
  timestamp: number;
  sender_id: string;
}

type EventHandler = (event: WSEvent) => void;

class WebSocketClient {
  private ws: WebSocket | null = null;
  private status: WebSocketStatus = 'closed';
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectDelayMs = 2000;
  private pingInterval: ReturnType<typeof setInterval> | null = null;
  private reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  private handlers: Map<WSEventType, Set<EventHandler>> = new Map();
  private pendingMessages: string[] = [];

  // Status getter
  getStatus(): WebSocketStatus {
    return this.status;
  }

  // ============================================================================
  // LIFECYCLE
  // ============================================================================

  /**
   * Connect to the WebSocket server and join a sending session.
   */
  connect(sessionId?: string): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      return;
    }

    const tokens = useAuthStore.getState().tokens;
    if (!tokens) {
      this.status = 'error';
      return;
    }

    this.status = 'connecting';

    const url = new URL(`${WS_BASE_URL}/session`);
    url.searchParams.set('token', tokens.access_token);
    if (sessionId) {
      url.searchParams.set('session_id', sessionId);
    }

    try {
      this.ws = new WebSocket(url.toString());
      this.setupHandlers(sessionId);
    } catch {
      this.status = 'error';
      this.scheduleReconnect(sessionId);
    }
  }

  /**
   * Gracefully close the connection.
   */
  disconnect(): void {
    this.clearPing();
    this.clearReconnect();

    if (this.ws) {
      this.status = 'closing';
      this.ws.close(1000, 'Client disconnect');
      this.ws = null;
    }

    this.status = 'closed';
    this.reconnectAttempts = 0;
  }

  // ============================================================================
  // HANDLERS
  // ============================================================================

  private setupHandlers(sessionId?: string): void {
    if (!this.ws) return;

    this.ws.onopen = () => {
      this.status = 'open';
      this.reconnectAttempts = 0;
      useSyncStore.getState().setRealtimeConnected(true);

      // Flush pending messages
      while (this.pendingMessages.length > 0) {
        const msg = this.pendingMessages.shift();
        if (msg) this.ws.send(msg);
      }

      this.startPing();
    };

    this.ws.onmessage = (event: MessageEvent) => {
      try {
        const wsEvent = JSON.parse(event.data as string) as WSEvent;
        this.dispatch(wsEvent);
      } catch {
        // Ignore malformed messages
      }
    };

    this.ws.onerror = () => {
      this.status = 'error';
      useSyncStore.getState().setRealtimeConnected(false);
    };

    this.ws.onclose = () => {
      const wasOpen = this.status === 'open';
      this.status = 'closed';
      this.ws = null;
      this.clearPing();
      useSyncStore.getState().setRealtimeConnected(false);

      if (wasOpen) {
        this.scheduleReconnect(sessionId);
      }
    };
  }

  // ============================================================================
  // PING / KEEPALIVE
  // ============================================================================

  private startPing(): void {
    this.clearPing();
    this.pingInterval = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        this.ws.send(JSON.stringify({ type: 'ping', timestamp: Date.now() }));
      }
    }, 30000);
  }

  private clearPing(): void {
    if (this.pingInterval) {
      clearInterval(this.pingInterval);
      this.pingInterval = null;
    }
  }

  // ============================================================================
  // RECONNECT
  // ============================================================================

  private scheduleReconnect(sessionId?: string): void {
    this.clearReconnect();

    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      return;
    }

    const delay = this.reconnectDelayMs * Math.pow(2, this.reconnectAttempts);
    this.reconnectAttempts++;

    this.reconnectTimeout = setTimeout(() => {
      this.connect(sessionId);
    }, Math.min(delay, 30000));
  }

  private clearReconnect(): void {
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = null;
    }
  }

  // ============================================================================
  // EVENT SUBSCRIPTION
  // ============================================================================

  /**
   * Subscribe to a WebSocket event type.
   */
  on(eventType: WSEventType, handler: EventHandler): () => void {
    if (!this.handlers.has(eventType)) {
      this.handlers.set(eventType, new Set());
    }
    this.handlers.get(eventType)!.add(handler);

    // Return unsubscribe function
    return () => {
      this.handlers.get(eventType)?.delete(handler);
    };
  }

  private dispatch(event: WSEvent): void {
    const handlers = this.handlers.get(event.type);
    if (handlers) {
      for (const handler of handlers) {
        try {
          handler(event);
        } catch {
          // Handler errors should not crash the WS client
        }
      }
    }
  }

  // ============================================================================
  // SEND MESSAGES
  // ============================================================================

  /**
   * Send a typed event to the server.
   */
  send(type: WSEventType, payload: unknown): void {
    const message = JSON.stringify({
      type,
      payload,
      timestamp: Date.now(),
    });

    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(message);
    } else {
      this.pendingMessages.push(message);
    }
  }

  /**
   * Broadcast a draft update during a sending session.
   */
  sendDraftUpdate(draftId: string, body: string, subjectLine?: string): void {
    this.send('draft.updated', {
      draft_id: draftId,
      body,
      subject_line: subjectLine,
    });
  }

  /**
   * Signal draft approval.
   */
  sendDraftApproved(draftId: string, cardId: string): void {
    this.send('draft.approved', {
      draft_id: draftId,
      card_id: cardId,
    });
  }

  /**
   * Join a voice session for a card.
   */
  joinVoiceSession(cardId: string): void {
    this.send('voice.state_change', {
      card_id: cardId,
      action: 'join_voice',
    });
  }
}

// Singleton instance
export const wsClient = new WebSocketClient();
