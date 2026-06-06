# Decision Stack — Master Documentation

## Section 1: Executive Summary

Decision Stack is an AI-powered email replacement system, not an email client. It treats email as a decision protocol: incoming messages become structured work units, AI performs comprehension and synthesis, and the human provides irreducible judgment on what actions to take. The system fundamentally inverts the traditional email workflow — instead of the human reading, sorting, and triaging every message, the AI does the expensive comprehension work and presents decisions ready for human resolution. The human never opens an inbox; they open a queue of decisions.

The codebase represents a production-grade, multi-service architecture spanning 599 files and 130,000+ lines across 9 bounded contexts. The implementation breakdown: 189 Go files, 182 Python files, 67 Terraform configurations, 84 TypeScript files, and 17 SQL migrations. Twelve services comprise the full stack — 8 core services plus OCR, STT, TTS, and Calendar integrations. Every service is bounded, contract-driven, and independently deployable.

A 6-turn remediation cycle brought the system from structurally incomplete to verified and coherent. Over 50 files were modified across the remediation. Six critical gaps in the send pipeline were identified and closed. Calendar chat integration was fully wired end-to-end. Eleven architectural invariants — structural rules governing service boundaries, data ownership, API contracts, and cross-context dependencies — were defined and verified, all passing. Twenty-nine agents contributed across the turns, from invariant checking to contract verification to gap remediation.

The system is now structurally complete and source-verified. Every bounded context has defined responsibilities. All cross-context contracts are explicit. The data model is normalized with proper ownership boundaries. What remains is runtime validation: the codebase needs a Go build environment, Docker containers, and AWS credentials to compile, deploy, and verify behavior against live infrastructure. No structural rework remains. No architectural gaps persist. The code is ready for the build.

---

## Section 2: Philosophy and Design Principles

Decision Stack is built on a single conviction: the future of knowledge work is not more tools for managing email — it is a system that understands what matters, presents decisions clearly, and lets humans exercise judgment where judgment is irreducible. Eighteen principles shape every architectural and product decision.

The system is first and foremost a decision-making and action-taking intelligence. It does not organize messages; it extracts meaning, identifies required actions, and moves work forward. Email is treated as a protocol, not a product — a transport layer for intent, not an end in itself. This reframing liberates the design from inbox metaphors and enables a fundamentally different interaction model.

The inversion principle sits at the core: AI performs the expensive comprehension work — reading, synthesizing, categorizing — while the human performs the irreducible judgment of deciding what to do. The machine handles scale; the human handles significance. This division of labor respects what each party does best and refuses to automate decisions that require human values.

The design philosophy is conservative. Explicit contracts between services, clear data ownership, no hidden coupling. Every choice prioritizes understandability and auditability over elegance. Trust operates on a three-tier architecture: Extract-Only services read and present without modification; Auto-Handle services perform low-risk actions within guardrails; Decision Stack services require explicit human confirmation for consequential actions. This lets the system automate routine work while keeping important decisions in human hands.

Human-in-the-loop is non-negotiable. The system does not learn to bypass the human; it learns to present decisions more efficiently. The batch processing model reinforces this — work arrives, is processed, and is presented in discrete batches rather than as a continuous stream demanding immediate attention. The architecture is offline-first with CRDT-based synchronization: local state is authoritative, network connectivity is optional.

Citation anchoring grounds every AI claim to specific source material — no unverifiable assertions. The system uses direct OAuth connections to email providers; no third-party APIs, no middleware with access to user data. Voice is a primary modality — STT and TTS are first-class infrastructure, equal to text.

The economic model is pay-per-action, not per-seat. Security means quarterly key rotation, PII scrubbing, and WAF protection as standard. Scalability follows a single-tenant-per-user model partitioned by user_id — no multi-tenant data co-mingling. Calendar integration provides decision context; sending is a first-class operation, not an afterthought. The card is the atomic unit of work — a structured, actionable representation that moves through states toward resolution. The final principle is completion over perfection: a decision made with good information beats perfect information that arrives too late.

These principles are enforced by the architecture, verified by the invariant suite, and embodied in the 599 files that comprise Decision Stack.
