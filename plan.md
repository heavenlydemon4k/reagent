# Corrective Execution Plan — Decision Stack

## Independent Assessment

The directive is correct: the system is architecturally sound but not shippable. The core issue is that the poller pipeline (fetch → parse → persist → publish) is complete and well-built, but the **adapter and fetcher stubs** at the integration layer return `fmt.Errorf("stub implementation")`. Four stubs block all real email flow:

1. **Token Store Adapter** — `oauth.TokenStore` has `LoadTokens()` but lacks the `GetTokens()` and `RefreshIfNeeded()` methods required by `poll.TokenStore` interface
2. **MIME Parser Adapter** — `parse.Parser.Parse()` signature `(ctx, raw, userID, accountID, receivedAt)` doesn't match `poll.MIMEParser` interface `(raw, accountID, userID)` — missing parameters need bridging
3. **Gmail Fetcher** — No real Gmail API client; stub returns error on every call
4. **Outlook Fetcher** — No real Graph API client; stub returns error on every call

These are 4 parallel, well-defined tasks. The poller logic itself (gap detection, rate limiting, pagination, progress saving) is production-quality and does not need modification.

## Execution Strategy

### Phase 1: Critical Path — Make Ingestion Real (HARD GATE)

**Track 1.1:** Token Store Adapter — Add `GetTokens()` + `RefreshIfNeeded()` to `oauth.TokenStore`
**Track 1.2:** MIME Parser Adapter — Bridge signature mismatch between `parse.Parser` and `poll.MIMEParser`
**Track 1.3:** Gmail Fetcher — Real `google.golang.org/api/gmail/v1` implementation
**Track 1.4:** Outlook Fetcher — Real Microsoft Graph API implementation

All 4 tracks are independent and can run in parallel. After completion, wire into `cmd/worker/main.go`.

### Phase 1b: Wire Main + End-to-End (Sequential after 1.1-1.4)
Replace all 4 stubs in `cmd/worker/main.go` with real implementations. Remove stub types. Verify compilation.

### Phase 2-5: Deferred until Phase 1 Gate passes
Per directive: "None of the following phases begin until Phase 1 verification is 100% complete."

### DECISIONS (required by directive):
- **5.6 Qdrant Cloud vs self-hosted:** Recommend Qdrant Cloud managed (3-node, ~$300/mo) for launch. Zero ops overhead. Migrate to self-hosted at 5,000+ users.
- **5.7 Partitioning strategy:** Recommend HASH(user_id) with 16 partitions. Even distribution, natural pruning on user-scoped queries. No hot partitions.
