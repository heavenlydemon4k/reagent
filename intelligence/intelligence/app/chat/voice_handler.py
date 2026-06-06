"""
Voice Handler — Audio input/output pipeline for Chat.

Converts the audio flow:
    User audio → Deepgram STT (Nova-2) → text → ChatService →
    response text → ElevenLabs TTS → audio file → S3 → presigned URL
"""

from __future__ import annotations

import logging
import tempfile
import time
from typing import Optional
from uuid import uuid4

from intelligence.app.chat.models import ChatResponse
from intelligence.app.chat.service import ChatService
from intelligence.app.logutil.sanitizer import get_sanitizer
from intelligence.core.config import get_settings

logger = logging.getLogger(__name__)
_sanitizer = get_sanitizer()


class VoiceHandler:
    """Handles voice input/output for chat."""

    def __init__(
        self,
        chat_service: ChatService,
        deepgram_client=None,
        elevenlabs_client=None,
        s3_client=None,
    ) -> None:
        self.chat = chat_service
        self.deepgram = deepgram_client
        self.elevenlabs = elevenlabs_client
        self.s3 = s3_client

    async def process_voice_input(
        self,
        audio_data: bytes,
        user_id: str,
        conversation_id: Optional[str] = None,
        linked_card_id: Optional[str] = None,
        voice_id: Optional[str] = None,
    ) -> ChatResponse:
        """
        Process a voice message end-to-end: STT → chat → TTS.

        Pipeline:
            1. Send audio to Deepgram Nova-2 streaming STT.
            2. Get transcription text.
            3. Process as text message via ChatService.
            4. Generate TTS for the response via ElevenLabs.
            5. Upload audio to S3.
            6. Return ChatResponse with audio_url.

        Args:
            audio_data: Raw audio bytes (wav, mp3, or m4a).
            user_id: Multi-tenancy user identifier.
            conversation_id: Existing conversation, or None for new.
            linked_card_id: Optional card to scope context.
            voice_id: ElevenLabs voice ID (uses default if None).

        Returns:
            ChatResponse with the assistant's text response and audio_url.
        """
        t0 = time.perf_counter()

        # 1. Speech-to-text via Deepgram
        transcription = await self._stt(audio_data)
        if not transcription or not transcription.strip():
            logger.warning("STT produced empty transcription")
            from intelligence.app.chat.models import ChatMessage

            empty_msg = ChatMessage(
                conversation_id=__import__("uuid").uuid4(),
                role="assistant",
                content="I couldn't understand the audio. Could you please repeat that?",
            )
            return ChatResponse(
                message=empty_msg,
                conversation_id=empty_msg.conversation_id,
            )

        logger.debug("STT result: %s", _sanitizer.redact_generic(transcription, 20))

        # 2. Process through ChatService (same as text message)
        response = await self.chat.send_message(
            user_id=user_id,
            message=transcription,
            conversation_id=conversation_id,
            linked_card_id=linked_card_id,
        )

        # 3. Text-to-speech for the response
        try:
            audio_url = await self.generate_tts(
                text=response.message.content,
                voice_id=voice_id,
            )
            response.message.audio_url = audio_url
            response.audio_url = audio_url
        except Exception as exc:
            logger.error("TTS generation failed: %s", exc, exc_info=True)
            # Return text-only response; audio_url stays None

        latency_ms = int((time.perf_counter() - t0) * 1000)
        response.latency_ms = latency_ms

        logger.info(
            "Voice pipeline completed latency=%dms stt_len=%d",
            latency_ms,
            len(transcription),
        )
        return response

    async def generate_tts(
        self,
        text: str,
        voice_id: Optional[str] = None,
    ) -> str:
        """
        Generate TTS audio for text and upload to S3.

        Uses ElevenLabs Turbo v2.5 for fast generation, then uploads
        the resulting audio to S3 and returns a presigned URL.

        Args:
            text: The text to synthesize.
            voice_id: ElevenLabs voice ID. Uses default if None.

        Returns:
            Presigned S3 URL for the generated audio file.
        """
        settings = get_settings()
        voice = voice_id or settings.elevenlabs_voice_id or "XB0fDUnXU5powFXDhCwa"

        if self.elevenlabs is None:
            raise RuntimeError("ElevenLabs client not configured")

        # Generate TTS audio
        audio_bytes = await self.elevenlabs.generate(
            text=text,
            voice_id=voice,
            model_id="eleven_turbo_v2_5",
        )

        if not audio_bytes:
            raise RuntimeError("TTS generation returned empty audio")

        # Upload to S3
        if self.s3 is None:
            raise RuntimeError("S3 client not configured")

        bucket = settings.s3_audio_bucket or "decisionstack-audio"
        key = f"tts/{__import__('datetime').datetime.utcnow().strftime('%Y/%m/%d')}/{uuid4()}.mp3"

        await self.s3.put_object(
            Bucket=bucket,
            Key=key,
            Body=audio_bytes,
            ContentType="audio/mpeg",
        )

        # Generate presigned URL (valid for 1 hour)
        url = await self.s3.generate_presigned_url(
            "get_object",
            Params={"Bucket": bucket, "Key": key},
            ExpiresIn=3600,
        )

        logger.debug("TTS audio uploaded to s3://%s/%s", bucket, key)
        return url

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    async def _stt(self, audio_data: bytes) -> str:
        """
        Transcribe audio data using Deepgram Nova-2.

        Args:
            audio_data: Raw audio bytes.

        Returns:
            Transcribed text, or empty string on failure.
        """
        if self.deepgram is None:
            logger.warning("Deepgram client not configured")
            return ""

        try:
            # Deepgram REST API
            from deepgram import (
                DeepgramClient,
                PrerecordedOptions,
                FileSource,
            )

            # Write audio to temp file for Deepgram SDK
            with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
                tmp.write(audio_data)
                tmp_path = tmp.name

            try:
                payload: FileSource = {"buffer": audio_data}
                options = PrerecordedOptions(
                    model="nova-2",
                    language="en",
                    punctuate=True,
                    smart_format=True,
                )
                response = self.deepgram.listen.prerecorded.v("1").transcribe_file(
                    payload,
                    options,
                )

                # Extract transcript
                results = response.results
                if results and results.channels:
                    alternatives = results.channels[0].alternatives
                    if alternatives:
                        transcript = alternatives[0].transcript
                        return transcript or ""
                return ""
            finally:
                import os
                try:
                    os.unlink(tmp_path)
                except OSError:
                    pass

        except Exception as exc:
            logger.error("STT failed: %s", exc, exc_info=True)
            return ""
