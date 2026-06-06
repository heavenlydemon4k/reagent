package auto

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/decisionstack/classification/internal/models"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ActionExecutor performs the configured action for a matched auto-handle rule.
type ActionExecutor struct {
	db           *sql.DB
	log          *slog.Logger
	ingestionConn *grpc.ClientConn
}

// ingestionMeshClient is the gRPC client interface for the Ingestion Mesh.
// In production, this would be the generated proto client.
type ingestionMeshClient interface {
	SendDraftReply(ctx context.Context, req *DraftReplyRequest) (*DraftReplyResponse, error)
	ForwardEmail(ctx context.Context, req *ForwardEmailRequest) (*ForwardEmailResponse, error)
	AcceptCalendarInvite(ctx context.Context, req *CalendarAcceptRequest) (*CalendarAcceptResponse, error)
	MarkForDeletion(ctx context.Context, req *DeleteRequest) (*DeleteResponse, error)
	SendPushNotification(ctx context.Context, req *PushNotificationRequest) (*PushNotificationResponse, error)
}

// DraftReplyRequest requests drafting a reply from a template.
type DraftReplyRequest struct {
	RawEmailID uuid.UUID
	UserID     uuid.UUID
	Template   string
	Variables  map[string]string
}

// DraftReplyResponse confirms a draft was created.
type DraftReplyResponse struct {
	DraftEmailID uuid.UUID
	Status       string
}

// ForwardEmailRequest requests forwarding an email.
type ForwardEmailRequest struct {
	RawEmailID      uuid.UUID
	UserID          uuid.UUID
	ForwardTo       string
	IncludeAttachments bool
}

// ForwardEmailResponse confirms an email was forwarded.
type ForwardEmailResponse struct {
	Status string
}

// CalendarAcceptRequest requests accepting a calendar invite.
type CalendarAcceptRequest struct {
	RawEmailID uuid.UUID
	UserID     uuid.UUID
	ThreadID   uuid.UUID
}

// CalendarAcceptResponse confirms a calendar invite was accepted.
type CalendarAcceptResponse struct {
	Status string
}

// DeleteRequest requests marking an email for deletion.
type DeleteRequest struct {
	RawEmailID uuid.UUID
	UserID     uuid.UUID
	ThreadID   uuid.UUID
}

// DeleteResponse confirms a deletion was marked.
type DeleteResponse struct {
	Status string
}

// PushNotificationRequest requests sending a push notification.
type PushNotificationRequest struct {
	UserID      uuid.UUID
	Title       string
	Body        string
	EmailRefID  uuid.UUID
}

// PushNotificationResponse confirms a push notification was sent.
type PushNotificationResponse struct {
	Status string
}

// NewActionExecutor creates a new ActionExecutor.
func NewActionExecutor(db *sql.DB, ingestionConn *grpc.ClientConn, log *slog.Logger) *ActionExecutor {
	return &ActionExecutor{
		db:            db,
		log:           log,
		ingestionConn: ingestionConn,
	}
}

// Execute runs the configured action for a matched rule.
// Returns an error only if the action itself fails; routing decisions are logged
// to decision_logs regardless of action success.
func (a *ActionExecutor) Execute(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent) error {
	start := time.Now().UTC()
	var actionErr error

	a.log.Info("executing auto-handle action",
		"rule_id", rule.ID,
		"rule_name", rule.Name,
		"action_type", rule.ActionType,
		"email_id", email.RawEmailID,
		"user_id", email.UserID,
	)

	switch rule.ActionType {
	case "reply_template":
		actionErr = a.executeReplyTemplate(ctx, rule, email)
	case "forward":
		actionErr = a.executeForward(ctx, rule, email)
	case "calendar_accept":
		actionErr = a.executeCalendarAccept(ctx, rule, email)
	case "delete":
		actionErr = a.executeDelete(ctx, rule, email)
	case "extract_notify":
		actionErr = a.executeExtractNotify(ctx, rule, email)
	default:
		actionErr = fmt.Errorf("unknown action type: %s", rule.ActionType)
	}

	elapsed := time.Since(start)

	// Always log to decision_logs.
	logErr := a.logDecision(ctx, rule, email, actionErr, elapsed)
	if logErr != nil {
		a.log.Error("failed to write decision log", "error", logErr)
	}

	// Publish AutoHandled event for downstream systems.
	publishErr := a.publishAutoHandled(ctx, rule, email, actionErr)
	if publishErr != nil {
		a.log.Error("failed to publish auto-handled event", "error", publishErr)
	}

	if actionErr != nil {
		return fmt.Errorf("action %s failed: %w", rule.ActionType, actionErr)
	}

	a.log.Info("auto-handle action completed",
		"rule_id", rule.ID,
		"action_type", rule.ActionType,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	return nil
}

// executeReplyTemplate drafts a reply from a template and sends via gRPC.
func (a *ActionExecutor) executeReplyTemplate(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent) error {
	var config struct {
		Template  string            `json:"template"`
		Variables map[string]string `json:"variables"`
	}
	if err := json.Unmarshal(rule.ActionConfig, &config); err != nil {
		return fmt.Errorf("parse reply_template config: %w", err)
	}

	a.log.Debug("drafting reply from template",
		"email_id", email.RawEmailID,
		"template", config.Template,
	)

	// In production, this would call the Ingestion Mesh gRPC service.
	// For now, log the intent and mark as processed.
	// TODO: connect to ingestionMeshClient once proto definitions are available.

	return nil
}

// executeForward forwards an email to a configured address.
func (a *ActionExecutor) executeForward(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent) error {
	var config struct {
		ForwardTo          string `json:"forward_to"`
		IncludeAttachments bool   `json:"include_attachments"`
	}
	if err := json.Unmarshal(rule.ActionConfig, &config); err != nil {
		return fmt.Errorf("parse forward config: %w", err)
	}

	if config.ForwardTo == "" {
		return fmt.Errorf("forward action missing forward_to address")
	}

	a.log.Debug("forwarding email",
		"email_id", email.RawEmailID,
		"forward_to", config.ForwardTo,
	)

	// TODO: call Ingestion Mesh gRPC ForwardEmail.

	return nil
}

// executeCalendarAccept accepts a calendar invite.
func (a *ActionExecutor) executeCalendarAccept(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent) error {
	a.log.Debug("accepting calendar invite",
		"email_id", email.RawEmailID,
		"thread_id", email.ThreadID,
	)

	// TODO: call Ingestion Mesh gRPC AcceptCalendarInvite.

	return nil
}

// executeDelete marks an email for deletion.
func (a *ActionExecutor) executeDelete(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent) error {
	a.log.Debug("marking email for deletion",
		"email_id", email.RawEmailID,
		"thread_id", email.ThreadID,
	)

	// TODO: call Ingestion Mesh gRPC MarkForDeletion.

	return nil
}

// executeExtractNotify sends a push notification with extracted data.
func (a *ActionExecutor) executeExtractNotify(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent) error {
	var config struct {
		NotificationTitle string `json:"notification_title"`
		ExtractField      string `json:"extract_field"` // "2fa" | "tracking" | "calendar" | "receipt"
	}
	if err := json.Unmarshal(rule.ActionConfig, &config); err != nil {
		return fmt.Errorf("parse extract_notify config: %w", err)
	}

	title := config.NotificationTitle
	if title == "" {
		title = fmt.Sprintf("Auto-handled: %s", rule.Name)
	}

	body := fmt.Sprintf("Email from %s auto-handled by rule %q", email.SenderEmail, rule.Name)

	a.log.Debug("sending push notification",
		"email_id", email.RawEmailID,
		"title", title,
	)

	// TODO: call push notification service via gRPC.
	_ = body

	return nil
}

// logDecision writes the action outcome to the decision_logs table.
func (a *ActionExecutor) logDecision(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent, actionErr error, elapsed time.Duration) error {
	const insertDecisionLogSQL = `
		INSERT INTO decision_logs (
			id, raw_email_id, user_id, thread_id, rule_id, rule_name,
			action_type, confidence, route, action_error, elapsed_ms, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	var errStr *string
	if actionErr != nil {
		s := actionErr.Error()
		errStr = &s
	}

	_, err := a.db.ExecContext(ctx, insertDecisionLogSQL,
		uuid.Must(uuid.NewRandom()),
		email.RawEmailID,
		email.UserID,
		email.ThreadID,
		rule.ID,
		rule.Name,
		rule.ActionType,
		rule.ConfidenceThreshold,
		string(models.RouteAuto),
		errStr,
		elapsed.Milliseconds(),
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert decision log: %w", err)
	}

	return nil
}

// publishAutoHandled publishes an AutoHandled event for downstream consumers.
func (a *ActionExecutor) publishAutoHandled(ctx context.Context, rule models.AutoHandleRule, email *models.EmailIngestedEvent, actionErr error) error {
	// TODO: publish to NATS or event bus once the messaging client is available.
	// The event should include:
	//   - EventID (new UUID)
	//   - RawEmailID
	//   - UserID
	//   - RuleID
	//   - RuleName
	//   - ActionType
	//   - ActionError (if any)
	//   - ProcessedAt

	return nil
}
