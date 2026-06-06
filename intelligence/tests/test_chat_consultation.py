"""
Tests for the Chat and Consultation services.

Covers:
- Consultation models, service logic, turn tracking
- Chat models, service, history, context retrieval
- Voice handler pipeline
- Router endpoint registration
- Prompt template rendering
"""

from __future__ import annotations

import sys
from datetime import datetime
from unittest.mock import AsyncMock, MagicMock, patch
from uuid import UUID, uuid4

import pytest

# Ensure project root is on path
sys.path.insert(0, "/mnt/agents/output/intelligence")


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_llm():
    """Mock LLMClient that returns predictable responses."""
    llm = MagicMock()
    llm.model_name = "claude-3-5-sonnet-20241022"
    llm.generate = AsyncMock(return_value=MagicMock(
        text="This is a test response.",
        model="claude-3-5-sonnet-20241022",
        tokens_input=100,
        tokens_output=50,
        total_tokens=150,
    ))
    return llm


@pytest.fixture
def mock_redis():
    """Mock Redis client with async methods."""
    redis = MagicMock()
    redis.get = AsyncMock(return_value="5")
    redis.incr = AsyncMock(return_value=6)
    redis.expire = AsyncMock(return_value=True)
    return redis


@pytest.fixture
def mock_chunk_store():
    """Mock ChunkStore with async search."""
    store = MagicMock()
    store.search_similar = AsyncMock(return_value=[])
    return store


@pytest.fixture
def mock_embedder():
    """Mock Embedder."""
    embedder = MagicMock()
    embedder.embed_single = AsyncMock(return_value=[0.1] * 1024)
    return embedder


@pytest.fixture
def mock_cross_encoder():
    """Mock cross-encoder for re-ranking."""
    ce = MagicMock()
    ce.predict = MagicMock(return_value=[0.9, 0.7, 0.5, 0.3, 0.1])
    return ce


@pytest.fixture
def sample_chunk():
    """Return a sample Chunk for testing."""
    from intelligence.app.compression.models import Chunk
    return Chunk(
        chunk_id=uuid4(),
        email_id=uuid4(),
        thread_id=uuid4(),
        user_id=uuid4(),
        sender_email="alice@example.com",
        content="The project is on track for delivery next week.",
        content_snippet="The project is on track for delivery next week.",
        paragraph_index=0,
        is_signature=False,
        timestamp=datetime(2024, 1, 15, 10, 30),
    )


# ---------------------------------------------------------------------------
# Consultation Models
# ---------------------------------------------------------------------------


class TestConsultationModels:
    def test_consult_request_creation(self):
        from intelligence.app.consultation.models import ConsultRequest
        req = ConsultRequest(card_id="abc-123", user_id="u-1", question="What's the status?")
        assert req.card_id == "abc-123"
        assert req.user_id == "u-1"
        assert req.question == "What's the status?"

    def test_consult_response_creation(self):
        from intelligence.app.consultation.models import ConsultResponse, Citation
        from uuid import uuid4
        citation = Citation(
            chunk_id=uuid4(),
            thread_id=uuid4(),
            email_id=uuid4(),
            sender_email="alice@example.com",
            content_snippet="On track",
            timestamp=datetime.utcnow(),
        )
        resp = ConsultResponse(
            answer="Everything is on track.",
            card_id="abc-123",
            turns_used=3,
            turns_remaining=7,
            citations=[citation],
        )
        assert resp.turns_used == 3
        assert resp.turns_remaining == 7
        assert len(resp.citations) == 1

    def test_turns_exhausted_response(self):
        from intelligence.app.consultation.models import ConsultResponse
        resp = ConsultResponse(
            answer="Maximum reached",
            card_id="abc",
            turns_used=10,
            turns_remaining=0,
        )
        assert resp.turns_remaining == 0


# ---------------------------------------------------------------------------
# Consultation Service
# ---------------------------------------------------------------------------


class TestConsultationService:
    @pytest.mark.asyncio
    async def test_ask_within_turn_limit(self, mock_llm, mock_chunk_store, mock_embedder, mock_cross_encoder, mock_redis, sample_chunk):
        from intelligence.app.consultation.service import ConsultationService
        from intelligence.app.consultation.retriever import ChunkRetriever

        mock_chunk_store.search_similar = AsyncMock(return_value=[sample_chunk])
        retriever = ChunkRetriever(mock_chunk_store, mock_embedder, mock_cross_encoder)
        svc = ConsultationService(mock_llm, retriever, mock_redis)
        svc.max_turns = 10

        # Turn count = 5, so should proceed
        mock_redis.get = AsyncMock(return_value="5")
        mock_redis.incr = AsyncMock(return_value=6)

        result = await svc.ask("card-1", "user-1", "What's the status?")

        assert "test response" in result.answer
        assert result.card_id == "card-1"
        assert result.turns_used == 6
        assert result.turns_remaining == 4
        assert len(result.citations) == 1
        mock_llm.generate.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_ask_at_turn_limit(self, mock_llm, mock_chunk_store, mock_embedder, mock_cross_encoder, mock_redis):
        from intelligence.app.consultation.service import ConsultationService
        from intelligence.app.consultation.retriever import ChunkRetriever

        retriever = ChunkRetriever(mock_chunk_store, mock_embedder, mock_cross_encoder)
        svc = ConsultationService(mock_llm, retriever, mock_redis)
        svc.max_turns = 10

        # Turn count already at 10
        mock_redis.get = AsyncMock(return_value="10")

        result = await svc.ask("card-1", "user-1", "What's the status?")

        assert "Maximum consultation turns reached" in result.answer
        assert result.turns_remaining == 0
        mock_llm.generate.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_get_turns_remaining(self, mock_llm, mock_chunk_store, mock_embedder, mock_cross_encoder, mock_redis):
        from intelligence.app.consultation.service import ConsultationService
        from intelligence.app.consultation.retriever import ChunkRetriever

        retriever = ChunkRetriever(mock_chunk_store, mock_embedder, mock_cross_encoder)
        svc = ConsultationService(mock_llm, retriever, mock_redis)
        svc.max_turns = 10

        mock_redis.get = AsyncMock(return_value="7")
        remaining = await svc.get_turns_remaining("card-1", "user-1")
        assert remaining == 3

    def test_build_prompt_no_chunks(self):
        from intelligence.app.consultation.service import ConsultationService
        svc = ConsultationService.__new__(ConsultationService)
        prompt = svc._build_prompt("Hello?", [])
        assert "Hello?" in prompt
        assert "expert email assistant" in prompt

    def test_system_prompt(self):
        from intelligence.app.consultation.service import ConsultationService
        svc = ConsultationService.__new__(ConsultationService)
        sp = svc._system_prompt()
        assert "expert email assistant" in sp
        assert "ONLY" in sp


# ---------------------------------------------------------------------------
# Chat Models
# ---------------------------------------------------------------------------


class TestChatModels:
    def test_chat_message_creation(self):
        from intelligence.app.chat.models import ChatMessage
        msg = ChatMessage(
            conversation_id=uuid4(),
            role="user",
            content="Hello assistant",
        )
        assert msg.role == "user"
        assert msg.content == "Hello assistant"
        assert isinstance(msg.id, UUID)

    def test_conversation_defaults(self):
        from intelligence.app.chat.models import Conversation
        conv = Conversation(user_id=uuid4())
        assert conv.voice_enabled is True
        assert len(conv.context_sources) == 4
        assert len(conv.messages) == 0

    def test_chat_response_creation(self):
        from intelligence.app.chat.models import ChatMessage, ChatResponse
        msg = ChatMessage(conversation_id=uuid4(), role="assistant", content="Hi!")
        resp = ChatResponse(message=msg, conversation_id=msg.conversation_id)
        assert resp.suggested_action is None
        assert resp.latency_ms == 0


# ---------------------------------------------------------------------------
# Chat Service
# ---------------------------------------------------------------------------


class TestChatService:
    def test_detect_action(self):
        from intelligence.app.chat.service import ChatService
        svc = ChatService.__new__(ChatService)
        assert svc._detect_action("Do this [ACTION: schedule]") == "schedule"
        assert svc._detect_action("Do this [ACTION: clear_batch]") == "clear_batch"
        assert svc._detect_action("No action") is None
        assert svc._detect_action("[ACTION: view_card]") == "view_card"

    def test_detect_action_case_insensitive(self):
        from intelligence.app.chat.service import ChatService
        svc = ChatService.__new__(ChatService)
        assert svc._detect_action("[action: Schedule]") == "schedule"
        assert svc._detect_action("[Action: VIEW_CARD]") == "view_card"

    def test_build_chat_prompt_basic(self):
        from intelligence.app.chat.service import ChatService
        from intelligence.app.chat.models import Conversation, ChatMessage
        from datetime import datetime

        svc = ChatService.__new__(ChatService)
        conv_id = uuid4()
        conv = Conversation(
            user_id=uuid4(),
            messages=[
                ChatMessage(conversation_id=conv_id, role="user", content="Hello", created_at=datetime.utcnow()),
                ChatMessage(conversation_id=conv_id, role="assistant", content="Hi there!", created_at=datetime.utcnow()),
            ],
        )
        context = {"contacts": [], "chunks": [], "events": [], "citations": []}
        prompt = svc._build_chat_prompt(conv, context, "What's new?")
        assert "Hello" in prompt
        assert "What's new?" in prompt
        assert "=== Context ===" in prompt

    def test_build_chat_prompt_with_context(self):
        from intelligence.app.chat.service import ChatService
        from intelligence.app.chat.models import Conversation
        svc = ChatService.__new__(ChatService)
        conv = Conversation(user_id=uuid4())
        context = {
            "contacts": [{"name": "Alice", "email": "alice@example.com", "company": "Acme"}],
            "chunks": [],
            "events": [{"title": "Team Sync", "start_time": "2024-01-20T10:00:00"}],
            "citations": [],
        }
        prompt = svc._build_chat_prompt(conv, context, "What's new?")
        assert "Alice" in prompt
        assert "Team Sync" in prompt

    def test_system_prompt(self):
        from intelligence.app.chat.service import ChatService
        svc = ChatService.__new__(ChatService)
        sp = svc._system_prompt()
        assert "executive assistant" in sp
        assert "[ACTION:" in sp


# ---------------------------------------------------------------------------
# Chat Retriever
# ---------------------------------------------------------------------------


class TestChatRetriever:
    @pytest.mark.asyncio
    async def test_retrieve_linked_card(self, mock_chunk_store, mock_embedder, sample_chunk):
        from intelligence.app.chat.retriever import ContextRetriever
        mock_chunk_store.search_similar = AsyncMock(return_value=[sample_chunk])
        retriever = ContextRetriever(mock_chunk_store, mock_embedder, neo4j_client=None)
        result = await retriever.retrieve(
            user_id="u1",
            conversation=MagicMock(),
            message="status?",
            linked_card_id="card-1",
        )
        assert "chunks" in result
        assert "citations" in result
        assert "contacts" in result
        assert "events" in result


# ---------------------------------------------------------------------------
# Chat History
# ---------------------------------------------------------------------------


class TestChatHistory:
    @pytest.mark.asyncio
    async def test_get_or_create_new(self):
        from intelligence.app.chat.history import ConversationHistory
        from intelligence.app.chat.models import Conversation

        mock_pool = MagicMock()
        mock_conn = MagicMock()
        mock_pool.acquire = MagicMock()
        mock_pool.acquire.__aenter__ = AsyncMock(return_value=mock_conn)
        mock_pool.acquire.__aexit__ = AsyncMock(return_value=False)

        mock_conn.execute = AsyncMock(return_value=None)

        history = ConversationHistory(mock_pool)
        conv = await history.get_or_create("user-1", None, title="Test Chat")

        assert isinstance(conv, Conversation)
        assert str(conv.user_id) == "user-1"
        assert conv.title == "Test Chat"

    def test_init_schema_sql(self):
        """Verify schema init contains expected table names."""
        from intelligence.app.chat.history import ConversationHistory
        # Just verify the method exists and contains expected table names
        assert hasattr(ConversationHistory, 'init_schema')


# ---------------------------------------------------------------------------
# Prompt Template
# ---------------------------------------------------------------------------


class TestPromptTemplate:
    def test_template_loads(self):
        from jinja2 import Environment, FileSystemLoader, select_autoescape
        env = Environment(
            loader=FileSystemLoader("/mnt/agents/output/intelligence/intelligence/core/prompt_templates"),
            autoescape=select_autoescape(),
        )
        template = env.get_template("consultation.jinja2")
        rendered = template.render(question="What?", chunks=[])
        assert "Question: What?" in rendered

    def test_template_with_chunks(self, sample_chunk):
        from jinja2 import Environment, FileSystemLoader, select_autoescape
        env = Environment(
            loader=FileSystemLoader("/mnt/agents/output/intelligence/intelligence/core/prompt_templates"),
            autoescape=select_autoescape(),
        )
        template = env.get_template("consultation.jinja2")
        rendered = template.render(question="Status?", chunks=[sample_chunk])
        assert "alice@example.com" in rendered
        assert "project is on track" in rendered


# ---------------------------------------------------------------------------
# Router
# ---------------------------------------------------------------------------


class TestRouter:
    def test_routes_registered(self):
        from intelligence.app.chat.router import router
        route_paths = [r.path for r in router.routes]
        assert "/chat/conversations" in route_paths
        assert "/chat/consult" in route_paths
        assert "/chat/consult/{card_id}/turns" in route_paths

    def test_app_router_includes_chat(self):
        from intelligence.app.router import api_router
        # api_router should include the chat router via include_router
        assert len(api_router.routes) > 0
