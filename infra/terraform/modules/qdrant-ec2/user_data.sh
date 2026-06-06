#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/qdrant-setup.log) 2>&1

echo "=== Decision Stack Qdrant Setup ==="

QDRANT_VERSION="${qdrant_version}"
ENV="${environment}"
EBS_DEVICE="/dev/${ebs_data_device}"
API_KEY="${api_key}"
VECSIZE_EMAIL="${collection_email_chunks_vector_size}"
VECSIZE_VOICE="${collection_voice_examples_vector_size}"
VECSIZE_CONSULT="${collection_consultation_index_vector_size}"
SECRETS_NAME="${secrets_manager_secret_name}"

DATA_DIR="/var/lib/qdrant"

# Format and mount EBS
mkdir -p "$DATA_DIR"
if ! mountpoint -q "$DATA_DIR"; then
  if ! blkid "$EBS_DEVICE" >/dev/null 2>&1; then
    mkfs -t xfs "$EBS_DEVICE"
  fi
  mount "$EBS_DEVICE" "$DATA_DIR"
  echo "$EBS_DEVICE $DATA_DIR xfs defaults,noatime 0 0" >> /etc/fstab
fi

# Install Docker
if ! command -v docker &>/dev/null; then
  dnf install -y docker
  systemctl enable docker
  systemctl start docker
fi

# Qdrant config
cat > "$DATA_DIR/config.yaml" <<QCONFIG
log_level: INFO

storage:
  storage_path: /qdrant/storage
  on_disk_payload: true
  performance:
    max_search_threads: 0
    max_optimization_threads: 0

service:
  http_port: 6333
  grpc_port: 6334
  api_key: "$API_KEY"
  enable_cors: true

cluster:
  enabled: false

telemetry_disabled: true
QCONFIG

# Run Qdrant
mkdir -p "$DATA_DIR/storage"
docker run -d \
  --name qdrant \
  --restart always \
  -p 6333:6333 \
  -p 6334:6334 \
  -v "$DATA_DIR/config.yaml:/qdrant/config/production.yaml" \
  -v "$DATA_DIR/storage:/qdrant/storage" \
  "qdrant/qdrant:$QDRANT_VERSION"

# Wait for Qdrant to be ready
sleep 5
until curl -fsS "http://localhost:6333/healthz" >/dev/null 2>&1; do
  echo "Waiting for Qdrant to be ready..."
  sleep 2
done

# Create collections
for collection in email_chunks voice_examples consultation_index; do
  vec_size="$VECSIZE_EMAIL"
  [ "$collection" = "voice_examples" ] && vec_size="$VECSIZE_VOICE"
  [ "$collection" = "consultation_index" ] && vec_size="$VECSIZE_CONSULT"

  curl -s -X PUT "http://localhost:6333/collections/$collection" \
    -H "Content-Type: application/json" \
    -H "api-key: $API_KEY" \
    -d "{
      \"vectors\": {
        \"size\": $vec_size,
        \"distance\": \"Cosine\",
        \"on_disk\": true
      },
      \"on_disk_payload\": true
    }" 2>/dev/null || echo "Collection $collection may already exist"
done

# Create payload indexes for multi-tenancy
for collection in email_chunks voice_examples consultation_index; do
  for field in user_id chunk_id thread_id email_id example_id sender_email; do
    curl -s -X PUT "http://localhost:6333/collections/$collection/index" \
      -H "Content-Type: application/json" \
      -H "api-key: $API_KEY" \
      -d "{\"field_name\": \"$field\", \"field_schema\": \"keyword\"}" 2>/dev/null || true
  done
done

# Store API key in Secrets Manager
aws secretsmanager put-secret-value \
  --secret-id "$SECRETS_NAME" \
  --secret-string "{\"api_key\": \"$API_KEY\"}" 2>/dev/null || true

echo "=== Qdrant Setup Complete ==="
