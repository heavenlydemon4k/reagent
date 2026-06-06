# Masterdoc — Sections 9-11: Remaining Work, Deployment, and Turn History

---

## Section 9: Remaining Work (Ranked by Priority)

### P0 — Must Fix Before Any Deployment

| Item | Problem | Fix | Effort |
|------|---------|-----|--------|
| Go build verification | No Go binary in sandbox to run `go build ./...` | Install Go 1.22 or run in CI | 10 min |
| Python app startup | `pip install` times out (network) | Complete stubs or use poetry offline | 10 min |
| Docker compose | No Docker in sandbox | Install Docker CE or run in CI | 10 min |

### P1 — Before Public Beta

| Item | Problem | Fix | Effort |
|------|---------|-----|--------|
| 5 orphaned NATS streams | Published but never consumed | Add consumers or remove publishers | Medium |
| ECS dual-architecture | `task_definitions.tf` conflicts with `main.tf` | Remove competing file | Small |
| Prod only 3/8 services | Missing ingestion, classification, sync, etc. | Add service variables | Medium |
| Client SSE streaming | Uses sync POST, no EventSource | Add EventSource for streaming | Small |

### P2 — Before 1,000 Users

| Item | Problem | Fix | Effort |
|------|---------|-----|--------|
| Qdrant clustering | Single instance | Migrate to managed or cluster | Medium |
| Neo4j HA | Single instance | AuraDS or Causal Cluster | Medium |
| `raw_emails` partitioning | Monthly partitions defined but not swapped | Execute maintenance window | Medium |
| Circuit breaker on LLM | Unbounded LLM consumption | Add circuit breaker | Small |

### P3 — Nice to Have

- Contact profile timeline from Neo4j
- Keyboard shortcuts (8 shortcuts already implemented)
- Dark mode (already implemented)
- Streak tracking (already implemented)
- Undo send (already implemented)

---

## Section 10: Deployment Quick Reference

### One-Command Summary

```bash
# 1. Terraform infrastructure
cd infra/terraform/environments/staging
terraform init && terraform apply

# 2. Database migrations
cd ingestion  && migrate -path migrations -database "$DATABASE_URL" up
cd sync       && migrate -path migrations -database "$DATABASE_URL" up
cd intelligence && alembic upgrade head

# 3. Start services
cd infra/docker && docker compose up -d

# 4. Verify health
./tests/integration/full_loop_test.sh --health-check-only

# 5. Seed + test
./tests/integration/full_loop_test.sh
./tests/integration/security_test.sh
```

### Docker Services (8 Total)

| Service | Role |
|---------|------|
| PostgreSQL | Primary datastore — emails, contacts, tasks, calendar |
| Redis | Caching, session store, job queue backing |
| NATS | Event bus — 14 streams, cross-service messaging |
| Qdrant | Vector store — email + contact embeddings |
| Neo4j | Graph — contact relationships, timeline queries |
| ingestion | Email fetch, parse, normalize, publish |
| classification | ML-based email classification + routing |
| intelligence | LLM features — summarization, drafting, extraction |
| sync | Bidirectional sync (Google/Microsoft) |
| OCR | Document text extraction pipeline |

### Environments

| Environment | Purpose | Cost Profile |
|-------------|---------|--------------|
| **dev** | Local workstation, hot-reload | Free (local Docker) |
| **staging** | Integration testing, scaled-down | ~12% of prod cost |
| **prod** | Full HA — multi-AZ, replication, backups | Production scale |

---

## Section 11: Complete Turn History

| Turn | Agents | Tasks | Theme | Outcome |
|------|--------|-------|-------|---------|
| 0 | 1 | 1 | Audit / Discovery | Found v2 repo (670 files vs. 482 in v1). Rewrote plan. |
| 1 | 4 | 4 | Foundation | Extract pipeline wired, CI paths fixed, client deps added, routes registered. 6 files. |
| 2 | 4 | 4 | Verification | Go build errors identified, 5 test scripts (2,095 lines), Python stubs, TS verified. 15 files. |
| 3 | 3 | 3 | Invariants | 8 Go compilation errors fixed, contact router fixed, **11/11 invariants PASS**. 9 files. |
| 4 | 6 | 6 | Critical Gaps | Send consumer built, send providers, calendar chat wired, chat commands, tests, audit. 16 files. |
| 5 | 6 | 18 | Multi-Fix | 5/6 send gaps closed, calendar COMPLETE, structured tools, regression tests, runbook (743 lines). 15 files. |
| 6 | 6 | 14 | Final Closure | **Gap 1 CLOSED** (6/6), orphaned streams, adapter tests, Python cleanup, CI + Terraform, client matrix. 12 files. |

### Cumulative Totals

| Metric | Value |
|--------|-------|
| Agents deployed | 29+ |
| Files modified / created | 50+ |
| Lines of tests + documentation | 4,701 |

---

*End of Sections 9-11*
