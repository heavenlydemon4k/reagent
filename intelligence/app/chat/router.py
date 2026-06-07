"""Chat API routes — sessions, messages, WebSocket, source fetch."""

from fastapi import APIRouter, WebSocket, WebSocketDisconnect
from typing import Optional

from intelligence.app.chat.service import ChatService
from intelligence.app.email_kb.service import EmailKnowledgeBase

router = APIRouter()
chat_service = ChatService()


@router.post("/sessions")
async def create_session(user_id: str, title: Optional[str] = "New Session", context: Optional[dict] = None):
    session = chat_service.create_session(user_id, title, context)
    return {"id": session.id, "title": session.title, "created_at": session.created_at}


@router.get("/sessions")
async def list_sessions(user_id: str):
    return chat_service.list_sessions(user_id)


@router.get("/sessions/{session_id}")
async def get_session(session_id: str):
    history = chat_service.get_history(session_id)
    return {"session_id": session_id, "messages": history}


@router.post("/sessions/{session_id}/messages")
async def send_message(session_id: str, user_id: str, content: str):
    result = chat_service.send_message(session_id, user_id, content)
    return result


@router.post("/sessions/{session_id}/cards")
async def send_card(session_id: str, card_data: dict):
    msg = chat_service.send_card(session_id, card_data)
    return msg


@router.get("/emails/{email_id}/source")
async def get_email_source(email_id: str, user_id: str):
    """Fetch original email for source verification."""
    kb = EmailKnowledgeBase()
    thread = kb.thread_context(email_id)
    if not thread:
        return {"error": "Email not found"}
    email = thread[0]
    return {
        "id": email.email_id,
        "subject": email.subject,
        "from": email.from_address,
        "to": email.to_addresses,
        "body_text": email.body_text,
        "received_at": email.received_at,
        "labels": email.labels,
    }


@router.websocket("/ws/{session_id}")
async def chat_websocket(websocket: WebSocket, session_id: str):
    await websocket.accept()
    try:
        while True:
            data = await websocket.receive_json()
            user_id = data.get("user_id")
            content = data.get("content")
            result = chat_service.send_message(session_id, user_id, content)
            await websocket.send_json(result)
    except WebSocketDisconnect:
        pass
