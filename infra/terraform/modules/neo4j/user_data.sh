#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/neo4j-setup.log) 2>&1

echo "=== Decision Stack Neo4j Setup ==="

NEO4J_VERSION="${neo4j_version}"
ENV="${environment}"
NEO4J_PASSWORD="${neo4j_password}"
LICENSE_KEY="${license_key}"
ENABLE_APOC="${enable_apoc}"
ENABLE_GDS="${enable_gds}"
HEAP_INITIAL="${memory_heap_initial}"
HEAP_MAX="${memory_heap_max}"
PAGECACHE="${memory_pagecache}"
DATA_DEVICE="/dev/${ebs_data_device}"
LOGS_DEVICE="/dev/${ebs_logs_device}"
SECRETS_NAME="${secrets_manager_name}"

NEO4J_DATA="/var/lib/neo4j/data"
NEO4J_LOGS="/var/log/neo4j"

# Format and mount EBS volumes
format_and_mount() {
  local device="$1" mountpoint="$2"
  mkdir -p "$mountpoint"
  if ! mountpoint -q "$mountpoint"; then
    if ! blkid "$device" >/dev/null 2>&1; then
      mkfs -t ext4 "$device"
    fi
    mount "$device" "$mountpoint"
    echo "$device $mountpoint ext4 defaults,noatime 0 0" >> /etc/fstab
  fi
}

format_and_mount "$DATA_DEVICE" "$NEO4J_DATA"
format_and_mount "$LOGS_DEVICE" "$NEO4J_LOGS"

# Install Docker
if ! command -v docker &>/dev/null; then
  dnf install -y docker
  systemctl enable docker
  systemctl start docker
fi

# Pull plugins
PLUGINS=""
[ "$ENABLE_APOC" = "true" ] && PLUGINS="apoc"
[ "$ENABLE_GDS" = "true" ] && PLUGINS="$PLUGINS,graph-data-science"

# License file if provided
LICENSE_MOUNT=""
if [ -n "$LICENSE_KEY" ] && [ "$LICENSE_KEY" != "" ]; then
  echo "$LICENSE_KEY" | base64 -d > /tmp/neo4j.license 2>/dev/null || echo "$LICENSE_KEY" > /tmp/neo4j.license
  LICENSE_MOUNT="-v /tmp/neo4j.license:/licenses/neo4j.license"
fi

# Run Neo4j Enterprise
docker run -d \
  --name neo4j \
  --restart always \
  -p 7474:7474 -p 7687:7687 \
  -e NEO4J_AUTH="neo4j/$NEO4J_PASSWORD" \
  -e NEO4J_dbms_memory_heap_initial__size="$HEAP_INITIAL" \
  -e NEO4J_dbms_memory_heap_max__size="$HEAP_MAX" \
  -e NEO4J_dbms_memory_pagecache_size="$PAGECACHE" \
  -e NEO4J_dbms_security_procedures_unrestricted="apoc.*,gds.*" \
  -e NEO4J_dbms_security_procedures_allowlist="apoc.*,gds.*" \
  -e NEO4J_apoc_export_file_enabled=true \
  -e NEO4J_apoc_import_file_enabled=true \
  -e NEO4J_dbms_default__database=neo4j \
  -e NEO4J_PLUGINS="[$PLUGINS]" \
  $LICENSE_MOUNT \
  -v "$NEO4J_DATA:/data" \
  -v "$NEO4J_LOGS:/logs" \
  "neo4j:$NEO4J_VERSION-enterprise"

# Wait for Neo4j to be ready
sleep 10
until curl -fsS -u "neo4j:$NEO4J_PASSWORD" "http://localhost:7474/dbms/health" >/dev/null 2>&1; do
  echo "Waiting for Neo4j to be ready..."
  sleep 3
done

# Create constraints
sleep 5
curl -s -u "neo4j:$NEO4J_PASSWORD" -X POST \
  "http://localhost:7474/db/neo4j/tx/commit" \
  -H "Content-Type: application/json" \
  -d '{
    "statements": [
      {"statement": "CREATE CONSTRAINT contact_email IF NOT EXISTS FOR (c:Contact) REQUIRE c.canonical_email IS UNIQUE"},
      {"statement": "CREATE CONSTRAINT contact_id IF NOT EXISTS FOR (c:Contact) REQUIRE c.id IS UNIQUE"}
    ]
  }' 2>/dev/null || echo "Constraints may already exist"

# Store credentials in Secrets Manager
aws secretsmanager put-secret-value \
  --secret-id "$SECRETS_NAME" \
  --secret-string "{\"username\": \"neo4j\", \"password\": \"$NEO4J_PASSWORD\"}" 2>/dev/null || true

echo "=== Neo4j Setup Complete ==="
