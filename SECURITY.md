# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 2.0.x   | :white_check_mark: |
| 1.0.x   | :x:                |

## Reporting a Vulnerability

Decision Stack handles OAuth tokens, email content, and calendar data.
Security issues should be reported privately.

**Email:** security@decisionstack.io

Please include:
- Service affected (ingestion, classification, intelligence, sync, client)
- Steps to reproduce
- Impact assessment

## Security Features

- OAuth tokens encrypted at rest (KMS DEK)
- JWT signing with auto-rotation
- SQL injection resistance (parameterized queries)
- WebSocket auth via JWT query param
- Rate limiting on auth endpoints
- CORS policy enforcement
