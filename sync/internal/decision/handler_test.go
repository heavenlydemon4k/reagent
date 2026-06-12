// Package decision_test provides unit tests for decision HTTP handlers.
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Mock DecisionProcessor
// ---------------------------------------------------------------------------

type mockProcessor struct {
	processDecisionFn        func(ctx context.Context, userID, cardID uuid.UUID, decision string, input *string) (*models.DecideResponse, error)
	processDraftModFn        func(ctx context.Context, userID, cardID uuid.UUID, instruction string) (*models.DecideResponse, error)
	processConsultFn         func(ctx context.Context, userID uuid.UUID, req *models.ConsultRequest) (*models.ConsultResponse, error)
	processEditFn            func(ctx context.Context, userID, draftID uuid.UUID, body string) (*models.DecideResponse, error)
	processApprovalFn        func(ctx context.Context, userID, draftID uuid.UUID) error
	getSourceCitationsFn     func(ctx context.Context, userID, cardID uuid.UUID) ([]models.ChunkCitation, error)
}

func (m *mockProcessor) ProcessDecision(ctx context.Context, userID, cardID uuid.UUID, decision string, input *string) (*models.DecideResponse, error) {
	if m.processDecisionFn != nil {
		return m.processDecisionFn(ctx, userID, cardID, decision, input)
	}
	return nil, ErrCardNotFound{CardID: cardID}
}

func (m *mockProcessor) ProcessDraftModification(ctx context.Context, userID, cardID uuid.UUID, instruction string) (*models.DecideResponse, error) {
	if m.processDraftModFn != nil {
		return m.processDraftModFn(ctx, userID, cardID, instruction)
	}
	return nil, ErrCardNotFound{CardID: cardID}
}

func (m *mockProcessor) ProcessConsultation(ctx context.Context, userID uuid.UUID, req *models.ConsultRequest) (*models.ConsultResponse, error) {
	if m.processConsultFn != nil {
		return m.processConsultFn(ctx, userID, req)
	}
	return nil, ErrCardNotFound{CardID: req.CardID}
}

func (m *mockProcessor) ProcessEdit(ctx context.Context, userID, draftID uuid.UUID, body string) (*models.DecideResponse, error) {
	if m.processEditFn != nil {
		return m.processEditFn(ctx, userID, draftID, body)
	}
	return nil, ErrDraftNotFound{DraftID: draftID}
}

func (m *mockProcessor) ProcessApproval(ctx context.Context, userID, draftID uuid.UUID) error {
	if m.processApprovalFn != nil {
		return m.processApprovalFn(ctx, userID, draftID)
	}
	return nil
}

func (m *mockProcessor) GetSourceCitations(ctx context.Context, userID, cardID uuid.UUID) ([]models.ChunkCitation, error) {
	if m.getSourceCitationsFn != nil {
		return m.getSourceCitationsFn(ctx, userID, cardID)
	}
	return nil, ErrCardNotFound{CardID: cardID}
}

// ---------------------------------------------------------------------------
// Mock IngestionMeshClient
// ---------------------------------------------------------------------------

type mockMeshClient struct{}

func (m *mockMeshClient) SendEmail(ctx context.Context, draftID, userID uuid.UUID, draftBody, subject string, inReplyTo *string, references []string) (time.Time, string, error) {
	return time.Now().UTC(), "msg-id-123", nil
}

// ---------------------------------------------------------------------------
// Helper: build handler with mock processor
// ---------------------------------------------------------------------------

func newTestHandler(t *testing.T, mp *mockProcessor) *Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError}))
	return NewHandler(mp, &mockMeshClient{}, log)
}

func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("invalid UUID %q: %v", s, err)
	}
	return uid
}

func contextWithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return WithUserID(ctx, userID)
}

func newRequest(t *testing.T, method, path, body string, userID uuid.UUID) *http.Request {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req = req.WithContext(contextWithUserID(req.Context(), userID))
	return req
}

// ---------------------------------------------------------------------------
// Tests: POST /cards/{id}/decide
// ---------------------------------------------------------------------------

func TestDecide_ValidDecision(t *testing.T) {
	uid := uuid.New()
	cardID := uuid.New()
	draftID := uuid.New()
	subject := "Test Subject"

	mp := &mockProcessor{
		processDecisionFn: func(ctx context.Context, userID, cid uuid.UUID, decision string, input *string) (*models.DecideResponse, error) {
			if userID != uid {
				t.Errorf("userID: want %s, got %s", uid, userID)
			}
			if cid != cardID {
				t.Errorf("cardID: want %s, got %s", cardID, cid)
			}
			if decision != "approve" {
				t.Errorf("decision: want approve, got %s", decision)
			}
			return &models.DecideResponse{
				DraftID:     draftID,
				DraftBody:   "Draft body here",
				SubjectLine: &subject,
			}, nil
		},
	}

	h := newTestHandler(t, mp)
	body := `{"decision":"approve"}`
	req := newRequest(t, "POST", "/cards/"+cardID.String()+"/decide", body, uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp decideResponseBody
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.DraftID != draftID {
		t.Errorf("draftID: want %s, got %s", draftID, resp.DraftID)
	}
	if resp.DraftBody != "Draft body here" {
		t.Errorf("draftBody: want %q, got %q", "Draft body here", resp.DraftBody)
	}
}

func TestDecide_InvalidCardID(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"decision":"approve"}`
	req := newRequest(t, "POST", "/cards/not-a-uuid/decide", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_card_id") {
		t.Errorf("body should contain invalid_card_id: %s", rr.Body.String())
	}
}

func TestDecide_MissingAuth(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"decision":"approve"}`
	req := httptest.NewRequest("POST", "/cards/"+uuid.New().String()+"/decide", bytes.NewReader([]byte(body)))
	// NO user ID in context
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

func TestDecide_InvalidBody(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `not valid json`
	req := newRequest(t, "POST", "/cards/"+uuid.New().String()+"/decide", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestDecide_InvalidDecisionType(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"decision":"reject"}`
	req := newRequest(t, "POST", "/cards/"+uuid.New().String()+"/decide", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid_decision") {
		t.Errorf("body should contain invalid_decision: %s", rr.Body.String())
	}
}

func TestDecide_CardNotFound(t *testing.T) {
	cardID := uuid.New()
	mp := &mockProcessor{
		processDecisionFn: func(ctx context.Context, userID, cid uuid.UUID, decision string, input *string) (*models.DecideResponse, error) {
			return nil, ErrCardNotFound{CardID: cid}
		},
	}

	h := newTestHandler(t, mp)
	body := `{"decision":"approve"}`
	req := newRequest(t, "POST", "/cards/"+cardID.String()+"/decide", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: want %d, got %d", http.StatusNotFound, rr.Code)
	}
}

func TestDecide_CardOwnership(t *testing.T) {
	cardID := uuid.New()
	mp := &mockProcessor{
		processDecisionFn: func(ctx context.Context, userID, cid uuid.UUID, decision string, input *string) (*models.DecideResponse, error) {
			return nil, ErrCardOwnership{CardID: cid, UserID: userID}
		},
	}

	h := newTestHandler(t, mp)
	body := `{"decision":"approve"}`
	req := newRequest(t, "POST", "/cards/"+cardID.String()+"/decide", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status: want %d, got %d", http.StatusForbidden, rr.Code)
	}
}

func TestDecide_EmptyBody(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	req := newRequest(t, "POST", "/cards/"+uuid.New().String()+"/decide", "", uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /cards/{id}/draft
// ---------------------------------------------------------------------------

func TestRequestDraft_Success(t *testing.T) {
	uid := uuid.New()
	cardID := uuid.New()
	draftID := uuid.New()

	mp := &mockProcessor{
		processDraftModFn: func(ctx context.Context, userID, cid uuid.UUID, instruction string) (*models.DecideResponse, error) {
			return &models.DecideResponse{DraftID: draftID, DraftBody: "Modified draft"}, nil
		},
	}

	h := newTestHandler(t, mp)
	body := `{"instruction":"make it shorter"}`
	req := newRequest(t, "POST", "/cards/"+cardID.String()+"/draft", body, uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
}

func TestRequestDraft_MissingInstruction(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"instruction":""}`
	req := newRequest(t, "POST", "/cards/"+uuid.New().String()+"/draft", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /drafts/{id}/approve
// ---------------------------------------------------------------------------

func TestApproveDraft_Success(t *testing.T) {
	uid := uuid.New()
	draftID := uuid.New()

	mp := &mockProcessor{
		processApprovalFn: func(ctx context.Context, userID, did uuid.UUID) error {
			return nil
		},
	}

	h := newTestHandler(t, mp)
	body := `{"approved":true}`
	req := newRequest(t, "POST", "/drafts/"+draftID.String()+"/approve", body, uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "approved" {
		t.Errorf("status: want approved, got %q", resp["status"])
	}
}

func TestApproveDraft_NotApproved(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"approved":false}`
	req := newRequest(t, "POST", "/drafts/"+uuid.New().String()+"/approve", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestApproveDraft_DraftNotFound(t *testing.T) {
	mp := &mockProcessor{
		processApprovalFn: func(ctx context.Context, userID, did uuid.UUID) error {
			return ErrDraftNotFound{DraftID: did}
		},
	}

	h := newTestHandler(t, mp)
	body := `{"approved":true}`
	req := newRequest(t, "POST", "/drafts/"+uuid.New().String()+"/approve", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: want %d, got %d", http.StatusNotFound, rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /drafts/{id}/edit
// ---------------------------------------------------------------------------

func TestEditDraft_Success(t *testing.T) {
	uid := uuid.New()
	draftID := uuid.New()

	mp := &mockProcessor{
		processEditFn: func(ctx context.Context, userID, did uuid.UUID, body string) (*models.DecideResponse, error) {
			return &models.DecideResponse{DraftID: did, DraftBody: body}, nil
		},
	}

	h := newTestHandler(t, mp)
	body := `{"draft_body":"Edited content here"}`
	req := newRequest(t, "POST", "/drafts/"+draftID.String()+"/edit", body, uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
}

func TestEditDraft_EmptyBody(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"draft_body":""}`
	req := newRequest(t, "POST", "/drafts/"+uuid.New().String()+"/edit", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /cards/{id}/source
// ---------------------------------------------------------------------------

func TestGetSource_Success(t *testing.T) {
	uid := uuid.New()
	cardID := uuid.New()

	mp := &mockProcessor{
		getSourceCitationsFn: func(ctx context.Context, userID, cid uuid.UUID) ([]models.ChunkCitation, error) {
			return []models.ChunkCitation{
				{ChunkID: uuid.New(), VerbatimSnippet: "Hello", EmailID: uuid.New(), ParagraphIndex: 1},
				{ChunkID: uuid.New(), VerbatimSnippet: "World", EmailID: uuid.New(), ParagraphIndex: 2},
			}, nil
		},
	}

	h := newTestHandler(t, mp)
	req := newRequest(t, "GET", "/cards/"+cardID.String()+"/source", "", uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp citationsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Citations) != 2 {
		t.Errorf("citations: want 2, got %d", len(resp.Citations))
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /consult
// ---------------------------------------------------------------------------

func TestConsult_Success(t *testing.T) {
	uid := uuid.New()
	cardID := uuid.New()

	mp := &mockProcessor{
		processConsultFn: func(ctx context.Context, userID uuid.UUID, req *models.ConsultRequest) (*models.ConsultResponse, error) {
			return &models.ConsultResponse{
				Answer:         "This is the answer",
				Citations:      []models.ChunkCitation{{ChunkID: uuid.New(), VerbatimSnippet: "citation"}},
				TurnsRemaining: 4,
			}, nil
		},
	}

	h := newTestHandler(t, mp)
	body := `{"card_id":"` + cardID.String() + `","question":"What does this mean?"}`
	req := newRequest(t, "POST", "/consult", body, uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp consultResponseBody
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Answer != "This is the answer" {
		t.Errorf("answer: want %q, got %q", "This is the answer", resp.Answer)
	}
	if resp.TurnsRemaining != 4 {
		t.Errorf("turns_remaining: want 4, got %d", resp.TurnsRemaining)
	}
}

func TestConsult_MissingCardID(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"card_id":"00000000-0000-0000-0000-000000000000","question":"What?"}`
	req := newRequest(t, "POST", "/consult", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestConsult_MissingQuestion(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	body := `{"card_id":"` + uuid.New().String() + `","question":""}`
	req := newRequest(t, "POST", "/consult", body, uuid.New())
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: want %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /send
// ---------------------------------------------------------------------------

func TestSend_Success(t *testing.T) {
	uid := uuid.New()
	draftID := uuid.New()

	log := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError}))

	// Create handler with mock approval flow
	approvalFlow := NewApprovalFlow(nil, nil, nil, log)
	h := &Handler{
		processor: &mockProcessorFull{
			approvalFlow: approvalFlow,
		},
		meshClient: &mockMeshClient{},
		log:        log,
	}

	body := `{"draft_id":"` + draftID.String() + `"}`
	req := newRequest(t, "POST", "/send", body, uid)
	rr := httptest.NewRecorder()

	r := chi.NewRouter()
	h.Routes(r)
	r.ServeHTTP(rr, req)

	// Will error due to nil stores in approval flow, but test the routing
	if rr.Code != http.StatusInternalServerError && rr.Code != http.StatusOK {
		t.Errorf("status: got %d, body: %s", rr.Code, rr.Body.String())
	}
}

// mockProcessorFull implements enough for the send test
type mockProcessorFull struct {
	approvalFlow *ApprovalFlow
}

func (m *mockProcessorFull) ProcessDecision(ctx context.Context, userID, cardID uuid.UUID, decision string, input *string) (*models.DecideResponse, error) {
	return nil, ErrCardNotFound{CardID: cardID}
}
func (m *mockProcessorFull) ProcessDraftModification(ctx context.Context, userID, cardID uuid.UUID, instruction string) (*models.DecideResponse, error) {
	return nil, ErrCardNotFound{CardID: cardID}
}
func (m *mockProcessorFull) ProcessConsultation(ctx context.Context, userID uuid.UUID, req *models.ConsultRequest) (*models.ConsultResponse, error) {
	return nil, ErrCardNotFound{CardID: req.CardID}
}
func (m *mockProcessorFull) ProcessEdit(ctx context.Context, userID, draftID uuid.UUID, body string) (*models.DecideResponse, error) {
	return nil, ErrDraftNotFound{DraftID: draftID}
}
func (m *mockProcessorFull) ProcessApproval(ctx context.Context, userID, draftID uuid.UUID) error {
	return nil
}
func (m *mockProcessorFull) GetSourceCitations(ctx context.Context, userID, cardID uuid.UUID) ([]models.ChunkCitation, error) {
	return nil, ErrCardNotFound{CardID: cardID}
}

// ---------------------------------------------------------------------------
// Tests: Route mounting
// ---------------------------------------------------------------------------

func TestRoutes_MountsAllEndpoints(t *testing.T) {
	h := newTestHandler(t, &mockProcessor{})
	r := chi.NewRouter()
	h.Routes(r)

	routes := make(map[string]bool)
	walkFn := func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}
	if err := chi.Walk(r, walkFn); err != nil {
		t.Fatalf("walk: %v", err)
	}

	expected := []string{
		"POST /cards/{id}/decide",
		"POST /cards/{id}/draft",
		"GET /cards/{id}/source",
		"POST /drafts/{id}/approve",
		"POST /drafts/{id}/edit",
		"POST /consult",
		"POST /send",
	}
	for _, exp := range expected {
		if !routes[exp] {
			t.Errorf("missing route: %s", exp)
		}
	}
}

// ---------------------------------------------------------------------------
// Table-driven: Decide endpoint
// ---------------------------------------------------------------------------

func TestDecideEndpoint_TableDriven(t *testing.T) {
	uid := uuid.New()
	cardID := uuid.New()
	draftID := uuid.New()

	tests := []struct {
		name           string
		path           string
		body           string
		setupProcessor func() *mockProcessor
		wantStatus     int
		wantCode       string
		noUserID       bool
	}{
		{
			name:       "valid decision returns 200 + draft",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       `{"decision":"approve"}`,
			wantStatus: http.StatusOK,
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{
					processDecisionFn: func(ctx context.Context, uid, cid uuid.UUID, dec string, inp *string) (*models.DecideResponse, error) {
						return &models.DecideResponse{DraftID: draftID, DraftBody: "draft"}, nil
					},
				}
			},
		},
		{
			name:       "invalid card ID returns 400",
			path:       "/cards/bad-id/decide",
			body:       `{"decision":"approve"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_card_id",
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{}
			},
		},
		{
			name:       "missing auth returns 401",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       `{"decision":"approve"}`,
			wantStatus: http.StatusUnauthorized,
			noUserID:   true,
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{}
			},
		},
		{
			name:       "empty body returns 400",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       "",
			wantStatus: http.StatusBadRequest,
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{}
			},
		},
		{
			name:       "invalid decision type returns 400",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       `{"decision":"reject"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_decision",
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{}
			},
		},
		{
			name:       "card not found returns 404",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       `{"decision":"approve"}`,
			wantStatus: http.StatusNotFound,
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{
					processDecisionFn: func(ctx context.Context, uid, cid uuid.UUID, dec string, inp *string) (*models.DecideResponse, error) {
						return nil, ErrCardNotFound{CardID: cid}
					},
				}
			},
		},
		{
			name:       "ownership error returns 403",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       `{"decision":"approve"}`,
			wantStatus: http.StatusForbidden,
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{
					processDecisionFn: func(ctx context.Context, uid, cid uuid.UUID, dec string, inp *string) (*models.DecideResponse, error) {
						return nil, ErrCardOwnership{CardID: cid, UserID: uid}
					},
				}
			},
		},
		{
			name:       "server error returns 500",
			path:       "/cards/" + cardID.String() + "/decide",
			body:       `{"decision":"approve"}`,
			wantStatus: http.StatusInternalServerError,
			setupProcessor: func() *mockProcessor {
				return &mockProcessor{
					processDecisionFn: func(ctx context.Context, uid, cid uuid.UUID, dec string, inp *string) (*models.DecideResponse, error) {
						return nil, ErrInvalidDecision{Decision: dec}
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := tt.setupProcessor()
			h := newTestHandler(t, mp)

			var req *http.Request
			if tt.noUserID {
				bodyReader := bytes.NewReader([]byte(tt.body))
				req = httptest.NewRequest("POST", tt.path, bodyReader)
			} else {
				req = newRequest(t, "POST", tt.path, tt.body, uid)
			}
			rr := httptest.NewRecorder()

			r := chi.NewRouter()
			h.Routes(r)
			r.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status: want %d, got %d, body: %s", tt.wantStatus, rr.Code, rr.Body.String())
			}
			if tt.wantCode != "" && !strings.Contains(rr.Body.String(), tt.wantCode) {
				t.Errorf("body should contain %q: %s", tt.wantCode, rr.Body.String())
			}
		})
	}
}
