"""
WebSocket Stream Manager

Manages concurrent bidirectional streaming connections between clients
and Deepgram. Each client gets a dedicated task pair:
  - One task reads audio from the client WebSocket → forwards to Deepgram
  - One task reads transcription events from Deepgram → forwards to client

Features:
- Connection heartbeat (ping every 30s)
- Max connection duration enforcement (5 minutes)
- Graceful disconnect handling with final transcript preservation
- Concurrent stream limiting (max 100)
"""

from __future__ import annotations

import asyncio
import json
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Optional

from fastapi import WebSocket, WebSocketDisconnect

from app.deepgram_client import DeepgramClient, DeepgramLiveConnection
from app.models import (
    HeartbeatMessage,
    StreamingSTTChunk,
    StreamingSession,
    StreamChunkMessage,
)
from core.config import get_settings
from core.logging_config import get_logger

logger = get_logger("stream")


# ---------------------------------------------------------------------------
# Client connection state
# ---------------------------------------------------------------------------

@dataclass
class _ClientStreamState:
    """Internal mutable state for an active streaming session."""

    session: StreamingSession
    dg_connection: Optional[DeepgramLiveConnection] = None
    client_to_dg_task: Optional[asyncio.Task] = None
    dg_to_client_task: Optional[asyncio.Task] = None
    heartbeat_task: Optional[asyncio.Task] = None
    timeout_task: Optional[asyncio.Task] = None
    disconnect_event: asyncio.Event = field(default_factory=asyncio.Event)


# ---------------------------------------------------------------------------
# Stream Manager
# ---------------------------------------------------------------------------

class StreamManager:
    """
    Manages WebSocket connections to Deepgram for multiple concurrent clients.

    Each client connection spawns:
    - _client_to_deepgram: reads binary audio frames from client, sends to DG
    - _deepgram_to_client: reads DG transcript events, sends JSON to client
    - _heartbeat_sender: periodic ping to keep connection alive
    - _timeout_watcher: enforces max connection duration
    """

    def __init__(self, deepgram_client: DeepgramClient) -> None:
        self.dg = deepgram_client
        self._streams: dict[str, _ClientStreamState] = {}
        self._settings = get_settings()
        logger.info(
            f"StreamManager initialized: max_duration="
            f"{self._settings.STREAM_MAX_DURATION_SECONDS}s, "
            f"heartbeat={self._settings.STREAM_HEARTBEAT_INTERVAL_SECONDS}s"
        )

    @property
    def active_stream_count(self) -> int:
        return sum(
            1 for s in self._streams.values() if s.session.is_active
        )

    @property
    def active_sessions(self) -> list[StreamingSession]:
        return [
            s.session for s in self._streams.values() if s.session.is_active
        ]

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def start_stream(
        self,
        client_id: str,
        websocket: WebSocket,
        language: str = "en",
        sample_rate: int = 16000,
    ) -> None:
        """
        Start a new streaming session for a client.

        1. Accept WebSocket connection
        2. Create Deepgram live connection
        3. Start concurrent tasks for bidirectional streaming
        4. Wait for disconnect or timeout
        5. Clean up all resources
        """
        settings = self._settings

        # Check concurrent stream limit
        if self.active_stream_count >= settings.MAX_CONCURRENT_STREAMS:
            await websocket.close(
                code=1013,
                reason="Server at max concurrent stream capacity",
            )
            logger.warning("Rejected connection: max streams reached")
            return

        await websocket.accept()

        # Create session
        session = StreamingSession(
            session_id=str(uuid.uuid4()),
            client_id=client_id,
            language=language,
        )
        state = _ClientStreamState(session=session)
        self._streams[session.session_id] = state

        logger.info(
            f"Stream started: session={session.session_id}, "
            f"client={client_id}, language={language}"
        )

        try:
            # Create Deepgram live connection
            dg_conn = await self.dg.create_live_connection(
                language=language,
                sample_rate=sample_rate,
                encoding="linear16",
                channels=1,
                interim_results=True,
                enable_vad_events=True,
            )
            state.dg_connection = dg_conn

            # Start concurrent tasks
            state.client_to_dg_task = asyncio.create_task(
                self._client_to_deepgram(websocket, dg_conn, state),
                name=f"client-to-dg-{session.session_id}",
            )
            state.dg_to_client_task = asyncio.create_task(
                self._deepgram_to_client(dg_conn, websocket, state),
                name=f"dg-to-client-{session.session_id}",
            )
            state.heartbeat_task = asyncio.create_task(
                self._heartbeat_sender(websocket, state),
                name=f"heartbeat-{session.session_id}",
            )
            state.timeout_task = asyncio.create_task(
                self._timeout_watcher(state, websocket),
                name=f"timeout-{session.session_id}",
            )

            # Wait for any task to signal disconnect
            await state.disconnect_event.wait()

        except WebSocketDisconnect:
            logger.info(
                f"Client disconnected: session={session.session_id}"
            )
        except Exception as exc:
            logger.error(
                f"Stream error: session={session.session_id}: {exc}",
                exc_info=True,
            )
            try:
                await websocket.send_json({
                    "type": "error",
                    "data": {"message": str(exc)},
                })
            except Exception:
                pass
        finally:
            await self._cleanup(state, websocket)

    async def stop_stream(self, session_id: str) -> Optional[StreamingSession]:
        """Forcefully stop a stream by session ID."""
        state = self._streams.get(session_id)
        if state:
            state.disconnect_event.set()
            return state.session
        return None

    # ------------------------------------------------------------------
    # Background tasks
    # ------------------------------------------------------------------

    async def _client_to_deepgram(
        self,
        client_ws: WebSocket,
        dg_conn: DeepgramLiveConnection,
        state: _ClientStreamState,
    ) -> None:
        """
        Read binary audio chunks from client WebSocket and forward to Deepgram.

        Handles:
        - Binary audio frames
        - JSON init/control messages
        - Connection drops
        """
        session = state.session

        try:
            while not state.disconnect_event.is_set():
                # Receive with timeout so we can check disconnect_event
                try:
                    message = await asyncio.wait_for(
                        client_ws.receive(), timeout=1.0
                    )
                except asyncio.TimeoutError:
                    continue

                # Handle different message types
                if "bytes" in message:
                    audio_chunk = message["bytes"]
                    if audio_chunk:
                        dg_conn.send(audio_chunk)
                        session.last_activity_at = time.time()
                        session.chunks_processed += 1

                elif "text" in message:
                    # JSON control message
                    text = message["text"]
                    try:
                        data = json.loads(text)
                        msg_type = data.get("type", "")

                        if msg_type == "init":
                            logger.debug(
                                f"Client init: session={session.session_id}"
                            )
                        elif msg_type == "close":
                            logger.info(
                                f"Client requested close: "
                                f"session={session.session_id}"
                            )
                            state.disconnect_event.set()
                            break
                        elif msg_type == "ping":
                            await client_ws.send_json(
                                HeartbeatMessage().model_dump()
                            )

                    except json.JSONDecodeError:
                        logger.warning(
                            f"Invalid JSON from client: session="
                            f"{session.session_id}"
                        )

                elif "type" in message and message.get("type") == "websocket.disconnect":
                    logger.debug(
                        f"WebSocket disconnect frame: "
                        f"session={session.session_id}"
                    )
                    state.disconnect_event.set()
                    break

        except WebSocketDisconnect:
            logger.info(
                f"WebSocket disconnected (client→DG): "
                f"session={session.session_id}"
            )
            state.disconnect_event.set()
        except Exception as exc:
            logger.error(
                f"client_to_deepgram error: session={session.session_id}: "
                f"{exc}"
            )
            state.disconnect_event.set()

    async def _deepgram_to_client(
        self,
        dg_conn: DeepgramLiveConnection,
        client_ws: WebSocket,
        state: _ClientStreamState,
    ) -> None:
        """
        Read Deepgram transcript events and forward to client WebSocket.

        Forwards:
        - Transcript chunks (interim + final)
        - Utterance end events
        - Speech started events
        """
        session = state.session

        try:
            async for event in dg_conn.event_stream():
                if state.disconnect_event.is_set():
                    break

                event_type = event.get("type", "")

                if event_type == "UtteranceEnd":
                    await client_ws.send_json({
                        "type": "utterance_end",
                        "timestamp": time.time(),
                        "session_id": session.session_id,
                    })
                    continue

                if event_type == "SpeechStarted":
                    await client_ws.send_json({
                        "type": "speech_started",
                        "timestamp": time.time(),
                        "session_id": session.session_id,
                    })
                    continue

                # Regular transcript result
                chunk = self._map_event_to_chunk(event)
                if chunk:
                    # Track final transcripts for reconnect
                    if chunk.is_final:
                        session.last_final_transcript = chunk.text
                        session.last_final_timestamp = time.time()

                    msg = StreamChunkMessage(
                        type="transcript",
                        data=chunk,
                        session_id=session.session_id,
                    )
                    await client_ws.send_json(msg.model_dump())
                    session.last_activity_at = time.time()

        except Exception as exc:
            if not state.disconnect_event.is_set():
                logger.error(
                    f"deepgram_to_client error: session={session.session_id}: "
                    f"{exc}"
                )
            state.disconnect_event.set()

    async def _heartbeat_sender(
        self, client_ws: WebSocket, state: _ClientStreamState
    ) -> None:
        """Send periodic heartbeat pings to keep the connection alive."""
        interval = self._settings.STREAM_HEARTBEAT_INTERVAL_SECONDS

        try:
            while not state.disconnect_event.is_set():
                await asyncio.sleep(interval)
                if state.disconnect_event.is_set():
                    break

                try:
                    heartbeat = HeartbeatMessage()
                    await client_ws.send_json(heartbeat.model_dump())
                    logger.debug(
                        f"Heartbeat sent: session={state.session.session_id}"
                    )
                except Exception as exc:
                    logger.warning(
                        f"Failed to send heartbeat: "
                        f"session={state.session.session_id}: {exc}"
                    )
                    state.disconnect_event.set()
                    break

        except asyncio.CancelledError:
            pass

    async def _timeout_watcher(
        self, state: _ClientStreamState, client_ws: WebSocket
    ) -> None:
        """Enforce maximum connection duration."""
        max_duration = self._settings.STREAM_MAX_DURATION_SECONDS

        try:
            await asyncio.sleep(max_duration)

            if not state.disconnect_event.is_set():
                logger.info(
                    f"Connection timeout after {max_duration}s: "
                    f"session={state.session.session_id}"
                )
                await client_ws.send_json({
                    "type": "error",
                    "data": {
                        "message": f"Max connection duration ({max_duration}s) reached",
                        "code": "CONNECTION_TIMEOUT",
                        "last_final_transcript": state.session.last_final_transcript,
                        "last_final_timestamp": state.session.last_final_timestamp,
                    },
                })
                state.disconnect_event.set()

        except asyncio.CancelledError:
            pass

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _map_event_to_chunk(self, event: dict[str, Any]) -> Optional[StreamingSTTChunk]:
        """Map a Deepgram event dict to StreamingSTTChunk."""
        if event.get("type") != "Results":
            return None
        data = event.get("data")
        if not data:
            return None
        try:
            from app.deepgram_client import _map_streaming_result
            return _map_streaming_result(data)
        except Exception:
            return None

    async def _cleanup(
        self, state: _ClientStreamState, websocket: WebSocket
    ) -> None:
        """Clean up all resources for a stream session."""
        session_id = state.session.session_id
        state.session.is_active = False

        logger.info(f"Cleaning up stream: session={session_id}")

        # Signal disconnect
        state.disconnect_event.set()

        # Cancel tasks
        tasks = [
            state.client_to_dg_task,
            state.dg_to_client_task,
            state.heartbeat_task,
            state.timeout_task,
        ]
        for task in tasks:
            if task and not task.done():
                task.cancel()

        # Wait for tasks to finish
        await asyncio.gather(*[t for t in tasks if t], return_exceptions=True)

        # Close DeepGram connection
        if state.dg_connection and not state.dg_connection.closed:
            await state.dg_connection.finish()

        # Close WebSocket
        try:
            await websocket.close()
        except Exception:
            pass

        # Remove from registry
        self._streams.pop(session_id, None)

        logger.info(
            f"Stream cleaned up: session={session_id}, "
            f"chunks={state.session.chunks_processed}, "
            f"duration={(time.time() - state.session.connected_at.timestamp()):.1f}s"
        )
