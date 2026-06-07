"""Chat API routes — sessions, messages, WebSocket, source fetch."""

from fastapi import APIRouter, WebSocket, WebSocketDisconnect, Depends, Query
from typing import Optional

from intelligence.app.chat.service import ChatService
from intelligence.app.email_kb.service import EmailKnowledgeBase
from intelligence.app.auth import get_current_user, get_current_user_ws

router = APIRouter()
chat_service = ChatService()


@router.post("/sessions")
async def create_session(
    title: Optional[str] = "New Session",
    context: Optional[dict] = None,
    user_id: str = Depends(get_current_user),
):
    session = await chat_service.create_session(user_id, title, context)
    return {"id": session.id, "title": session.title, "created_at": session.created_at}


@router.get("/sessions")
async def list_sessions(user_id: str = Depends(get_current_user)):
    return await chat_service.list_sessions(user_id)


@router.get("/sessions/{session_id}")
async def get_session(session_id: str, user_id: str = Depends(get_current_user)):
    history = chat_service.get_history(session_id)
    return {"session_id": session_id, "messages": history}


@router.post("/sessions/{session_id}/messages")
async def send_message(session_id: str, content: str, user_id: str = Depends(get_current_user)):
    result = await chat_service.send_message(session_id, user_id, content)
    return result


@router.post("/sessions/{session_id}/cards")
async def send_card(session_id: str, card_data: dict, user_id: str = Depends(get_current_user)):
    msg = await chat_service.send_card(session_id, card_data)
    return msg


@router.get("/emails/{email_id}/source")
async def get_email_source(email_id: str, user_id: str = Depends(get_current_user)):
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
async def chat_websocket(websocket: WebSocket, session_id: str, token: str = Query(...)):
    """WebSocket with query-param JWT auth. Handles: message, card_action, source_request, pause_session, resume_session."""
    try:
        user_id = await get_current_user_ws(token)
    except Exception:
        await websocket.close(code=1008)
        return

    await websocket.accept()
    try:
        while True:
            data = await websocket.receive_json()
            msg_type = data.get("type", "message")

            if msg_type == "message":
                content = data.get("content", "")
                # Typing indicator before LLM call
                await websocket.send_json({"type": "typing", "session_id": session_id, "active": True})
                result = await chat_service.send_message(session_id, user_id, content)
                await websocket.send_json({"type": "typing", "session_id": session_id, "active": False})
                # Wrap result for client
                await websocket.send_json({"type": "message", "session_id": session_id, **result})

            elif msg_type == "card_action":
                card_id = data.get("card_id")
                action_id = data.get("action_id")
                payload = data.get("payload")
                result = await chat_service.handle_card_action(session_id, user_id, card_id, action_id, payload)
                await websocket.send_json({"type": "card_action_result", "session_id": session_id, **result})

            elif msg_type == "source_request":
                email_id = data.get("email_id")
                source = await chat_service.handle_source_request(session_id, email_id)
                await websocket.send_json({"type": "source_email", "session_id": session_id, "email_id": email_id, "email": source})

            elif msg_type == "pause_session":
                await websocket.send_json({"type": "session_paused", "session_id": session_id})

            elif msg_type == "resume_session":
                await websocket.send_json({"type": "session_resumed", "session_id": session_id})

            elif msg_type == "ping":
                await websocket.send_json({"type": "pong"})

            else:
                await websocket.send_json({"type": "error", "message": f"Unknown type: {msg_type}"})

    except WebSocketDisconnect:
        pass
    except Exception as e:
        try:
            await websocket.send_json({"type": "error", "message": str(e)})
        except:
            pass
        await websocket.close()
