import os
import shutil
import subprocess

# --- 1. Create .gitignore ---
gitignore = """# Dependencies
node_modules/
vendor/
.venv/
__pycache__/
*.pyc

# Build outputs
dist/
build/
.expo/
bin/
*.exe
*.dll

# Environment
.env
.env.local
.env.*.local

# IDE
.vscode/
.idea/
*.swp
*~

# OS
.DS_Store
Thumbs.db
desktop.ini

# Logs
*.log
logs/

# Terraform
*.tfstate
*.tfstate.*
.terraform/
.terraform.lock.hcl

# Test coverage
coverage/
*.out
*.coverage
coverage.xml

# Mobile
*.apk
*.ipa
*.aab

# Misc
*.tmp
*.temp
.cache/
"""

with open(".gitignore", "w", encoding="utf-8") as f:
    f.write(gitignore)

# --- 2. Move docs to docs/operations ---
os.makedirs("docs/operations", exist_ok=True)
docs_to_move = [
    "MASTER_STATE.md",
    "DEPLOYMENT.md",
    "FEATURE_MATRIX.md",
    "FILES_EDITED.md",
    "REPO_GUIDE.md",
    "plan.md"
]
for doc in docs_to_move:
    if os.path.exists(doc):
        shutil.move(doc, f"docs/operations/{doc}")

# --- 3. Create CHANGELOG.md ---
changelog = """# Changelog

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
"""

with open("CHANGELOG.md", "w", encoding="utf-8") as f:
    f.write(changelog)

# --- 4. Create SECURITY.md ---
security = """# Security Policy

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
"""

with open("SECURITY.md", "w", encoding="utf-8") as f:
    f.write(security)

# --- 5. Create .editorconfig ---
editorconfig = """root = true

[*]
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true
charset = utf-8

[*.go]
indent_style = tab

[*.py]
indent_style = space
indent_size = 4

[*.{ts,tsx,js,jsx,json,yml,yaml}]
indent_style = space
indent_size = 2

[*.md]
trim_trailing_whitespace = false

[Makefile]
indent_style = tab
"""

with open(".editorconfig", "w", encoding="utf-8") as f:
    f.write(editorconfig)

# --- 6. Create GitHub templates ---
os.makedirs(".github/ISSUE_TEMPLATE", exist_ok=True)

bug_report = """---
name: Bug report
about: Create a report to help us improve
title: '[BUG]'
labels: bug
assignees: ''
---

**Service affected**
- [ ] ingestion
- [ ] classification
- [ ] intelligence
- [ ] sync
- [ ] client
- [ ] OCR / STT / TTS / calendar
- [ ] infrastructure

**Describe the bug**
A clear description of what the bug is.

**To Reproduce**
Steps to reproduce the behavior.

**Expected behavior**
A clear description of what you expected to happen.

**Environment**
- OS: [e.g. Windows 11, macOS 14, Ubuntu 22.04]
- Node/Go/Python version:
- Branch:
"""

with open(".github/ISSUE_TEMPLATE/bug_report.md", "w", encoding="utf-8") as f:
    f.write(bug_report)

feature_request = """---
name: Feature request
about: Suggest an idea for this project
title: '[FEAT]'
labels: enhancement
assignees: ''
---

**Is your feature request related to a problem?**
A clear description of what the problem is.

**Describe the solution you'd like**
A clear description of what you want to happen.

**Which service(s)**
- [ ] ingestion
- [ ] classification
- [ ] intelligence
- [ ] sync
- [ ] client
- [ ] infrastructure
"""

with open(".github/ISSUE_TEMPLATE/feature_request.md", "w", encoding="utf-8") as f:
    f.write(feature_request)

invariant_violation = """---
name: Invariant Violation
about: Report a breach of architectural invariant
title: '[INVARIANT]'
labels: critical, architecture
assignees: ''
---

**Invariant violated**
- [ ] No inbox view
- [ ] No raw email on client
- [ ] Conservative routing 0.92
- [ ] 48-hour rule staging
- [ ] Human-in-the-loop
- [ ] Batch clearing only
- [ ] Other (specify below)

**Description**
How was the invariant violated? What behavior was observed?

**Service**
- [ ] ingestion
- [ ] classification
- [ ] intelligence
- [ ] sync
- [ ] client
"""

with open(".github/ISSUE_TEMPLATE/invariant_violation.md", "w", encoding="utf-8") as f:
    f.write(invariant_violation)

pr_template = """## What
Brief description of changes.

## Services Affected
- [ ] ingestion
- [ ] classification
- [ ] intelligence
- [ ] sync
- [ ] client
- [ ] services (OCR/STT/TTS/calendar)
- [ ] infrastructure

## Invariants
- [ ] All 11 invariants still pass
- [ ] New invariant added (document in MASTER_STATE.md)
- [ ] N/A

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Manual testing performed
- [ ] N/A

## Checklist
- [ ] Code follows project style guidelines (.editorconfig)
- [ ] Documentation updated (if needed)
- [ ] No duplicate files committed
- [ ] CI workflow paths verified
"""

with open(".github/pull_request_template.md", "w", encoding="utf-8") as f:
    f.write(pr_template)

print("All files created successfully.")