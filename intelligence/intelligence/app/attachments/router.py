"""
FastAPI routes for Attachment management.

Provides endpoints to list user attachments and generate time-limited
presigned URLs for secure download from S3 (SSE-KMS encrypted).
"""

from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import List, Optional
from uuid import UUID

import boto3
from botocore.exceptions import ClientError
from fastapi import APIRouter, Depends, Header, HTTPException, Query, status
from pydantic import BaseModel, Field

from intelligence.core.config import get_settings
from intelligence.core.db import get_connection

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/attachments", tags=["attachments"])

# S3 path pattern: attachments/{user_id}/{thread_id}/{filename}
PRESIGN_EXPIRY_SECONDS = 900  # 15 minutes

# ---------------------------------------------------------------------------
# S3 client (module-level singleton)
# ---------------------------------------------------------------------------

_s3_client: Optional[boto3.client] = None


def _get_s3_client() -> boto3.client:
    """Return the shared S3 client, creating it if necessary."""
    global _s3_client
    if _s3_client is None:
        settings = get_settings()
        session_kwargs = {"region_name": settings.aws_region}
        if settings.aws_access_key_id and settings.aws_secret_access_key:
            session_kwargs["aws_access_key_id"] = settings.aws_access_key_id
            session_kwargs["aws_secret_access_key"] = settings.aws_secret_access_key
        _s3_client = boto3.client("s3", **session_kwargs)
    return _s3_client


# ---------------------------------------------------------------------------
# Request/response schemas
# ---------------------------------------------------------------------------


class AttachmentListItem(BaseModel):
    """Lightweight attachment metadata for list views."""

    attachment_id: str
    thread_id: str
    filename: str
    content_type: str
    size_bytes: int
    s3_path: str
    created_at: datetime


class AttachmentListResponse(BaseModel):
    """Response for listing attachments."""

    attachments: List[AttachmentListItem]
    total: int


class PresignedUrlResponse(BaseModel):
    """Response containing a presigned S3 URL for download."""

    attachment_id: str
    filename: str
    download_url: str
    expires_in_seconds: int = Field(default=PRESIGN_EXPIRY_SECONDS)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


async def _fetch_attachment(attachment_id: str, user_id: str) -> Optional[dict]:
    """
    Fetch a single attachment row, verifying user ownership.

    Returns the attachment dict or None if not found / not owned.
    """
    async with get_connection() as conn:
        row = await conn.fetchrow(
            """
            SELECT id, user_id, thread_id, filename, content_type,
                   size_bytes, s3_path, created_at
            FROM attachments
            WHERE id = $1 AND user_id = $2
            """,
            attachment_id,
            user_id,
        )
    return dict(row) if row else None


async def _list_attachments_for_user(
    user_id: str,
    thread_id: Optional[str] = None,
    limit: int = 50,
    offset: int = 0,
) -> List[dict]:
    """List attachment metadata for a user, optionally filtered by thread."""
    async with get_connection() as conn:
        if thread_id:
            rows = await conn.fetch(
                """
                SELECT id, thread_id, filename, content_type,
                       size_bytes, s3_path, created_at
                FROM attachments
                WHERE user_id = $1 AND thread_id = $2
                ORDER BY created_at DESC
                LIMIT $3 OFFSET $4
                """,
                user_id,
                thread_id,
                limit,
                offset,
            )
        else:
            rows = await conn.fetch(
                """
                SELECT id, thread_id, filename, content_type,
                       size_bytes, s3_path, created_at
                FROM attachments
                WHERE user_id = $1
                ORDER BY created_at DESC
                LIMIT $2 OFFSET $3
                """,
                user_id,
                limit,
                offset,
            )
    return [dict(row) for row in rows]


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


@router.get("", response_model=AttachmentListResponse)
async def list_attachments(
    x_user_id: str = Header(..., alias="X-User-ID"),
    thread_id: Optional[str] = Query(None, description="Filter by thread UUID"),
    limit: int = Query(50, ge=1, le=200),
    offset: int = Query(0, ge=0),
):
    """
    List attachments for the authenticated user.

    Optionally filter by thread_id. Results are ordered by created_at DESC.

    Headers:
        X-User-ID: Authenticated user ID (required).

    Query:
        thread_id: Optional thread UUID to scope results.
        limit: Max results (1-200, default 50).
        offset: Pagination offset.

    Returns:
        AttachmentListResponse with attachment metadata.
    """
    if not x_user_id:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="X-User-ID header is required",
        )

    try:
        rows = await _list_attachments_for_user(
            user_id=x_user_id,
            thread_id=thread_id,
            limit=limit,
            offset=offset,
        )

        attachments = [
            AttachmentListItem(
                attachment_id=str(row["id"]),
                thread_id=str(row["thread_id"]),
                filename=row["filename"],
                content_type=row["content_type"],
                size_bytes=row["size_bytes"],
                s3_path=row["s3_path"],
                created_at=row["created_at"],
            )
            for row in rows
        ]

        return AttachmentListResponse(
            attachments=attachments,
            total=len(attachments),
        )
    except HTTPException:
        raise
    except Exception as exc:
        logger.error("Failed to list attachments: %s", exc, exc_info=True)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to list attachments",
        )


@router.get("/{attachment_id}/url", response_model=PresignedUrlResponse)
async def get_attachment_presigned_url(
    attachment_id: str,
    x_user_id: str = Header(..., alias="X-User-ID"),
):
    """
    Generate a presigned S3 URL to download an attachment.

    The URL is valid for 15 minutes and provides direct access to the
    SSE-KMS encrypted object in S3.

    Headers:
        X-User-ID: Authenticated user ID (required).

    Path:
        attachment_id: UUID of the attachment record.

    Returns:
        PresignedUrlResponse with the temporary download URL.

    Raises:
        404: Attachment not found or not owned by user.
        500: Failed to generate presigned URL.
    """
    if not x_user_id:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="X-User-ID header is required",
        )

    # Validate attachment exists and belongs to user
    attachment = await _fetch_attachment(attachment_id, x_user_id)
    if attachment is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Attachment not found",
        )

    # Generate presigned URL
    try:
        s3 = _get_s3_client()
        settings = get_settings()
        bucket = settings.s3_audio_bucket  # reuses configured S3 bucket
        s3_path = attachment["s3_path"]

        url = s3.generate_presigned_url(
            "get_object",
            Params={
                "Bucket": bucket,
                "Key": s3_path,
            },
            ExpiresIn=PRESIGN_EXPIRY_SECONDS,
        )

        return PresignedUrlResponse(
            attachment_id=attachment_id,
            filename=attachment["filename"],
            download_url=url,
        )
    except ClientError as exc:
        logger.error(
            "Failed to generate presigned URL for attachment %s: %s",
            attachment_id,
            exc,
            exc_info=True,
        )
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to generate download URL",
        )
    except Exception as exc:
        logger.error(
            "Unexpected error generating presigned URL: %s", exc, exc_info=True
        )
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Failed to generate download URL",
        )
