#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/nats-setup.log) 2>&1

echo "=== Decision Stack NATS 3-Node Cluster Setup ==="

# Variables from Terraform
NODE_INDEX="${node_index}"
NODE_NAME="${node_name}"
CLUSTER_NAME="${cluster_name}"
CLUSTER_ROUTES="${cluster_routes}"
CLUSTER_PEERS="${cluster_peers}"
NATS_VERSION="${nats_version}"
JETSTREAM_MAX_MEMORY="${jetstream_max_memory}"
JETSTREAM_MAX_FILE="${jetstream_max_file}"
ENV="${environment}"
NODE_IPS="${node_ips}"
USERS_JSON='${users_json}'

JETSTREAM_DEVICE="/dev/nvme1n1"
DATA_DIR="/data/jetstream"
NATS_CONFIG_DIR="/etc/nats"

echo "Node: $NODE_NAME (index $NODE_INDEX)"
echo "Cluster: $CLUSTER_NAME"
echo "Routes: $CLUSTER_ROUTES"

# ---------------------------------------------------------------------------
# Format and mount JetStream data volume
# ---------------------------------------------------------------------------
format_and_mount() {
  local device="$1"
  local mountpoint="$2"
  mkdir -p "$mountpoint"
  if ! mountpoint -q "$mountpoint"; then
    # Wait for device to appear
    for i in $(seq 1 30); do
      if [ -e "$device" ]; then
        break
      fi
      echo "Waiting for $device to appear... ($i/30)"
      sleep 2
    done
    if ! [ -e "$device" ]; then
      echo "WARNING: $device not found, trying /dev/xvdf"
      device="/dev/xvdf"
    fi
    if ! blkid "$device" >/dev/null 2>&1; then
      echo "Formatting $device as xfs..."
      mkfs -t xfs "$device"
    fi
    mount "$device" "$mountpoint"
    echo "$device $mountpoint xfs defaults,noatime 0 0" >> /etc/fstab
  fi
}

format_and_mount "$JETSTREAM_DEVICE" "$DATA_DIR"

# ---------------------------------------------------------------------------
# Create nats user
# ---------------------------------------------------------------------------
groupadd -r nats 2>/dev/null || true
useradd -r -g nats -d "$DATA_DIR" -s /usr/sbin/nologin nats 2>/dev/null || true
chown -R nats:nats "$DATA_DIR"

# ---------------------------------------------------------------------------
# Install NATS Server
# ---------------------------------------------------------------------------
if [ ! -f /usr/local/bin/nats-server ]; then
  echo "Installing NATS server v$NATS_VERSION..."
  curl -sf https://binaries.nats.dev/nats-io/nats-server/v2@"$NATS_VERSION" | sh
  mv nats-server /usr/local/bin/
  chmod +x /usr/local/bin/nats-server
fi

mkdir -p "$NATS_CONFIG_DIR"
mkdir -p /var/log/nats
chown nats:nats /var/log/nats

# ---------------------------------------------------------------------------
# Write /etc/hosts for cluster name resolution
# ---------------------------------------------------------------------------
echo "=== Setting up /etc/hosts for cluster DNS ==="
IFS=',' read -ra NODE_ENTRIES <<< "$NODE_IPS"
for entry in "${NODE_ENTRIES[@]}"; do
  ip="${entry%%=*}"
  name="${entry##*=}"
  # Remove existing entry
  sed -i "/$name$/d" /etc/hosts 2>/dev/null || true
  echo "$ip $name" >> /etc/hosts
  echo "  $ip -> $name"
done

# ---------------------------------------------------------------------------
# Build cluster routes block
# ---------------------------------------------------------------------------
IFS=',' read -ra ROUTES <<< "$CLUSTER_ROUTES"
ROUTES_BLOCK=""
for route in "${ROUTES[@]}"; do
  ROUTES_BLOCK="${ROUTES_BLOCK}\n    $route"
done

# ---------------------------------------------------------------------------
# Build resolver users config
# ---------------------------------------------------------------------------
echo "=== Building MEMORY resolver config ==="
RESOLVER_USERS=""
USER_COUNT=$(echo "$USERS_JSON" | jq '. | length')
for i in $(seq 0 $((USER_COUNT - 1))); do
  USER_OBJ=$(echo "$USERS_JSON" | jq -r ".[$i]")
  USERNAME=$(echo "$USER_OBJ" | jq -r '.username')
  PASSWORD=$(echo "$USER_OBJ" | jq -r '.password')
  PUB_ALLOW=$(echo "$USER_OBJ" | jq -r '.permissions.pub.allow | join(", ")')
  SUB_ALLOW=$(echo "$USER_OBJ" | jq -r '.permissions.sub.allow | join(", ")')

  RESOLVER_USERS="${RESOLVER_USERS}
    {
      user: \"$USERNAME\"
      pass: \"$PASSWORD\"
      permissions: {
        publish: {
          allow: [$PUB_ALLOW]
        }
        subscribe: {
          allow: [$SUB_ALLOW]
        }
      }
    }"
done

# ---------------------------------------------------------------------------
# Write NATS server configuration
# ---------------------------------------------------------------------------
echo "=== Writing NATS server.conf ==="
cat > "$NATS_CONFIG_DIR/server.conf" <<NATSCONFIG
# ---------------------------------------------------------------------------
# NATS Server Configuration — 3-Node Cluster with JetStream HA
# ---------------------------------------------------------------------------

# Server identity
server_name: "$NODE_NAME"
listen: 0.0.0.0:4222
http: 0.0.0.0:8222

# Max payload 8MB for large email threads
max_payload: 8MB

# Max connections
max_connections: 65536

# Logging
logfile: "/var/log/nats/nats.log"
log_size_limit: 100MB
logtime: true

# Disable leaf nodes
leafnodes {
  listen: ""
}

# ---------------------------------------------------------------------------
# Cluster Configuration — Full Mesh of 3 Nodes
# ---------------------------------------------------------------------------
cluster {
  name: "$CLUSTER_NAME"
  listen: 0.0.0.0:6222

  # All 3 routes for full mesh
  routes: [${ROUTES_BLOCK}
  ]

  # Authorization for cluster route connections
  authorization {
    user: "ruser"
    password: "$CLUSTER_NAME-route-auth"
    timeout: 2
  }

  # Connect retries
  connect_retries: 60
}

# ---------------------------------------------------------------------------
# JetStream Configuration — HA with 3 Replicas
# ---------------------------------------------------------------------------
jetstream {
  store_dir: "$DATA_DIR"
  max_memory_store: ${JETSTREAM_MAX_MEMORY}GB
  max_file_store: ${JETSTREAM_MAX_FILE}GB

  # Unique tag per node for placement
  unique_tag: "node:$NODE_NAME"
}

# ---------------------------------------------------------------------------
# Authentication — MEMORY Resolver
# ---------------------------------------------------------------------------
authorization {
  # No default auth — require explicit user
}

resolver: MEMORY

resolver_preload: {
  # Admin user with full permissions
  admin: $${ADMIN_HASH}
}
NATSCONFIG

# Append resolver users dynamically
# We use a here-doc to append the accounts/users
{
  echo ""
  echo "# ---------------------------------------------------------------------------"
  echo "# Accounts & Users (MEMORY Resolver)"
  echo "# ---------------------------------------------------------------------------"
  echo "accounts {"
  echo "  \\$G {"
  echo "    users: ["

  for i in $(seq 0 $((USER_COUNT - 1))); do
    USER_OBJ=$(echo "$USERS_JSON" | jq -r ".[$i]")
    USERNAME=$(echo "$USER_OBJ" | jq -r '.username')
    PASSWORD=$(echo "$USER_OBJ" | jq -r '.password')
    PUB_ALLOW=$(echo "$USER_OBJ" | jq -r '.permissions.pub.allow | @json')
    SUB_ALLOW=$(echo "$USER_OBJ" | jq -r '.permissions.sub.allow | @json')

    echo "      { user: \"$USERNAME\", pass: \"$PASSWORD\","
    echo "        permissions: {"
    echo "          publish: { allow: $PUB_ALLOW },"
    echo "          subscribe: { allow: $SUB_ALLOW }"
    echo "        }"
    echo "      }"
  done

  echo "    ]"
  echo "  }"
  echo "}"
} >> "$NATS_CONFIG_DIR/server.conf"

chown nats:nats "$NATS_CONFIG_DIR/server.conf"
chmod 640 "$NATS_CONFIG_DIR/server.conf"

# Debug: show final config (redact passwords)
echo "=== NATS Config (passwords redacted) ==="
sed 's/pass: ".*"/pass: "***"/g' "$NATS_CONFIG_DIR/server.conf"

# ---------------------------------------------------------------------------
# Systemd service unit
# ---------------------------------------------------------------------------
cat > /etc/systemd/system/nats-server.service <<'SERVICE'
[Unit]
Description=NATS Server — Decision Stack Cluster Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nats
Group=nats
ExecStart=/usr/local/bin/nats-server -c /etc/nats/server.conf
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
LimitNOFILE=65536
StandardOutput=append:/var/log/nats/nats.log
StandardError=append:/var/log/nats/nats.log

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
systemctl enable nats-server
systemctl start nats-server

# ---------------------------------------------------------------------------
# Install nats CLI
# ---------------------------------------------------------------------------
if [ ! -f /usr/local/bin/nats ]; then
  echo "Installing nats CLI..."
  curl -sf https://get-nats.io | sh
  mv nats /usr/local/bin/
fi

# ---------------------------------------------------------------------------
# Wait for NATS to be ready
# ---------------------------------------------------------------------------
echo "=== Waiting for NATS cluster to be ready ==="
sleep 10

MAX_WAIT=120
for i in $(seq 1 $MAX_WAIT); do
  if nats --server nats://localhost:4222 server check --js-enabled 2>/dev/null; then
    echo "NATS is ready!"
    break
  fi
  echo "Waiting for NATS... ($i/$MAX_WAIT)"
  sleep 2
done

# ---------------------------------------------------------------------------
# Report cluster status
# ---------------------------------------------------------------------------
echo "=== Cluster Status ==="
nats --server nats://localhost:4222 server list 2>/dev/null || echo "Cluster still forming..."

echo "=== JetStream Status ==="
nats --server nats://localhost:4222 server check --js-enabled --js-server-only 2>/dev/null || true

echo "=== NATS 3-Node Cluster Setup Complete ==="
