"""
Attachments API module.

Provides endpoints to list attachments and generate presigned S3 URLs
for secure download of SSE-KMS encrypted files.
"""

from intelligence.app.attachments.router import router

__all__ = ["router"]
