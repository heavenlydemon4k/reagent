package extract

// ExtractType identifies the category of extracted data in the Extract-Only pipeline.
type ExtractType string

const (
	// Type2FA is a one-time password or verification code.
	Type2FA ExtractType = "2fa"
	// TypeTracking is a shipment or package tracking number.
	TypeTracking ExtractType = "tracking"
	// TypeCalendar is a calendar invite or event.
	TypeCalendar ExtractType = "calendar"
	// TypeReceipt is a receipt, order confirmation, or invoice.
	TypeReceipt ExtractType = "receipt"
	// TypeNewsletter is a newsletter or mailing-list email.
	TypeNewsletter ExtractType = "newsletter"
	// TypeNotification is a generic notification (account alert, etc.).
	TypeNotification ExtractType = "notification"
)

// NotificationTemplates maps extract types to user-friendly notification text.
var NotificationTemplates = map[ExtractType]string{
	Type2FA:          "Verification code detected",
	TypeTracking:     "Package tracking update",
	TypeCalendar:     "Calendar event received",
	TypeReceipt:      "Receipt or order confirmation",
	TypeNewsletter:   "Newsletter received",
	TypeNotification: "Notification received",
}
