package nats

import (
	"fmt"

	natsgo "github.com/nats-io/nats.go"
)

// CheckNATS verifies that all 6 required JetStream streams exist and are healthy.
// It checks each stream defined in StreamConfigs and returns an error if any
// stream is missing or unhealthy.
func CheckNATS(js natsgo.JetStreamContext) error {
	if js == nil {
		return fmt.Errorf("jetstream context is nil")
	}

	streamNames := []string{
		"EMAIL_INGESTED",
		"EMAIL_INGESTED_DLQ",
		"INTELLIGENCE_COMPRESS",
		"EXTRACT_COMPLETED",
		"AUTO_HANDLED",
		"SYNC_NOTIFY_CARD_CREATED",
	}

	for _, name := range streamNames {
		info, err := js.StreamInfo(name)
		if err != nil {
			return fmt.Errorf("stream %s check failed: %w", name, err)
		}
		if info == nil {
			return fmt.Errorf("stream %s not found", name)
		}
		if info.Config.Name == "" {
			return fmt.Errorf("stream %s has empty config", name)
		}
	}

	return nil
}

// CheckNATSConnection verifies the NATS connection is alive.
func CheckNATSConnection(nc *natsgo.Conn) error {
	if nc == nil {
		return fmt.Errorf("nats connection is nil")
	}
	if !nc.IsConnected() {
		return fmt.Errorf("nats connection is not connected")
	}
	return nil
}
