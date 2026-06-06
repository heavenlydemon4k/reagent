# Changelog

All notable changes to this project will be documented in this file.

## [2.0.0] - 2026-06-07

### Added
- Complete email send pipeline (6 gaps closed)
- SyncNatsAdapter bridging sync approval to NATS JetStream
- SendConsumer for email.send dispatch with Gmail/Outlook support
- Calendar chat integration (4 endpoints, slash commands, voice intent)
- 14 Python stubs for offline/type-checking builds
- Integration test suites (full loop, security, send pipeline)
- Expo Application Services config (eas.json)
- TokenMeter with rate limiting
- LLM fallback chain with cost routing

### Fixed
- CI workflow path references (services/X -> X)
- 204 status code bug in contact router
- Import path fixes in ingestion crypto and mocks
- Duplicate Claims type in sync auth

### Changed
- EmailProvider.SendEmail now returns (string, error) for message_id
- Classification engine wired with Extractor interface
- Auto-Handle engine uses internal interface instead of gRPC

### Infrastructure
- Terraform FIXMEs documented for 5 missing ECS services
- FIXME added for dual-architecture task definition conflict
- CI now includes shared/logutil test step
