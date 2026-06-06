module github.com/decisionstack/tests/integration

go 1.22

require (
	github.com/decisionstack/ingestion v0.0.0
	github.com/decisionstack/sync v0.0.0
	github.com/google/uuid v1.6.0
)

replace (
	github.com/decisionstack/ingestion => ../../ingestion
	github.com/decisionstack/sync => ../../sync
)
