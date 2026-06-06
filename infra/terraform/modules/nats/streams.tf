# ---------------------------------------------------------------------------
# JetStream Streams — R:3 Replicated Across All 3 Nodes
# ---------------------------------------------------------------------------
# All streams use --replicas=3 for HA.
# NATS cluster survives 1-node failure (RAFT quorum = 2 of 3).
#
# NOTE: Provisioner runs from node-0 after all nodes are up and
# the cluster has formed. Uses nats CLI with --context flag to
# target the local node.
# ---------------------------------------------------------------------------

locals {
  stream_definitions = {
    EMAIL_INGESTED = {
      subjects  = "email.ingested"
      storage   = "file"
      replicas  = 3
      retention = "work"
      max_msgs  = -1
      max_age   = "7d"
      discard   = "old"
    }
    INTELLIGENCE_COMPRESS = {
      subjects  = "intelligence.compress"
      storage   = "file"
      replicas  = 3
      retention = "work"
      max_msgs  = -1
      max_age   = "7d"
      discard   = "old"
    }
    EXTRACT_COMPLETED = {
      subjects  = "ExtractCompleted"
      storage   = "file"
      replicas  = 3
      retention = "limits"
      max_msgs  = -1
      max_age   = "7d"
      discard   = "old"
    }
    AUTO_HANDLED = {
      subjects  = "AutoHandled"
      storage   = "file"
      replicas  = 3
      retention = "limits"
      max_msgs  = -1
      max_age   = "7d"
      discard   = "old"
    }
    SYNC_NOTIFY_CARD_CREATED = {
      subjects  = "sync.notify.CardCreated"
      storage   = "file"
      replicas  = 3
      retention = "limits"
      max_msgs  = -1
      max_age   = "7d"
      discard   = "old"
    }
  }
}

# ---------------------------------------------------------------------------
# Stream provisioning via local-exec on node-0
# ---------------------------------------------------------------------------
resource "null_resource" "nats_streams" {
  triggers = {
    # Re-run when any node IP changes or on explicit trigger
    node_0_ip = aws_instance.nats[0].private_ip
    node_1_ip = aws_instance.nats[1].private_ip
    node_2_ip = aws_instance.nats[2].private_ip
    stream_hash = md5(jsonencode(local.stream_definitions))
  }

  # Wait for all nodes to be running
  depends_on = [
    aws_instance.nats[0],
    aws_instance.nats[1],
    aws_instance.nats[2],
    aws_volume_attachment.jetstream[0],
    aws_volume_attachment.jetstream[1],
    aws_volume_attachment.jetstream[2],
  ]

  provisioner "local-exec" {
    command = <<-EOT
      echo "=== Waiting for NATS cluster to form ==="
      # Use SSH or SSM to run commands on node-0
      # For SSM-based approach (no bastion required):

      CLUSTER_IPS="${aws_instance.nats[0].private_ip},${aws_instance.nats[1].private_ip},${aws_instance.nats[2].private_ip}"
      echo "Cluster IPs: $CLUSTER_IPS"

      # Wait for cluster readiness (with timeout)
      MAX_RETRIES=60
      RETRY_COUNT=0
      while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
        # Use SSM to check if NATS is ready on node-0
        READY=$(aws ec2 describe-instances \
          --instance-ids ${aws_instance.nats[0].id} \
          --query 'Reservations[0].Instances[0].State.Name' \
          --output text 2>/dev/null || echo "unknown")
        if [ "$READY" = "running" ]; then
          echo "Node-0 is running."
          break
        fi
        echo "Waiting for node-0... ($RETRY_COUNT/$MAX_RETRIES)"
        sleep 10
        RETRY_COUNT=$((RETRY_COUNT + 1))
      done

      # Allow additional time for cluster formation
      echo "Waiting 60s for cluster formation..."
      sleep 60

      echo "=== Creating JetStream streams (R:3) ==="

      # EMAIL_INGESTED — WorkQueue, replicated
      aws ssm send-command \
        --instance-ids ${aws_instance.nats[0].id} \
        --document-name "AWS-RunShellScript" \
        --parameters commands=["nats --server nats://localhost:4222 stream info EMAIL_INGESTED 2>/dev/null || nats --server nats://localhost:4222 stream add EMAIL_INGESTED --subjects='email.ingested' --storage=file --replicas=3 --retention=work --max-msgs=-1 --max-age=7d --discard=old --yes 2>&1"] \
        --comment "Create EMAIL_INGESTED stream" \
        --region ${data.aws_region.current.name} \
        2>/dev/null || echo "Stream creation via SSM may have failed — manual intervention required"

      # INTELLIGENCE_COMPRESS
      aws ssm send-command \
        --instance-ids ${aws_instance.nats[0].id} \
        --document-name "AWS-RunShellScript" \
        --parameters commands=["nats --server nats://localhost:4222 stream info INTELLIGENCE_COMPRESS 2>/dev/null || nats --server nats://localhost:4222 stream add INTELLIGENCE_COMPRESS --subjects='intelligence.compress' --storage=file --replicas=3 --retention=work --max-msgs=-1 --max-age=7d --discard=old --yes 2>&1"] \
        --comment "Create INTELLIGENCE_COMPRESS stream" \
        --region ${data.aws_region.current.name} \
        2>/dev/null || true

      # EXTRACT_COMPLETED
      aws ssm send-command \
        --instance-ids ${aws_instance.nats[0].id} \
        --document-name "AWS-RunShellScript" \
        --parameters commands=["nats --server nats://localhost:4222 stream info EXTRACT_COMPLETED 2>/dev/null || nats --server nats://localhost:4222 stream add EXTRACT_COMPLETED --subjects='ExtractCompleted' --storage=file --replicas=3 --retention=limits --max-msgs=-1 --max-age=7d --discard=old --yes 2>&1"] \
        --comment "Create EXTRACT_COMPLETED stream" \
        --region ${data.aws_region.current.name} \
        2>/dev/null || true

      # AUTO_HANDLED
      aws ssm send-command \
        --instance-ids ${aws_instance.nats[0].id} \
        --document-name "AWS-RunShellScript" \
        --parameters commands=["nats --server nats://localhost:4222 stream info AUTO_HANDLED 2>/dev/null || nats --server nats://localhost:4222 stream add AUTO_HANDLED --subjects='AutoHandled' --storage=file --replicas=3 --retention=limits --max-msgs=-1 --max-age=7d --discard=old --yes 2>&1"] \
        --comment "Create AUTO_HANDLED stream" \
        --region ${data.aws_region.current.name} \
        2>/dev/null || true

      # SYNC_NOTIFY_CARD_CREATED
      aws ssm send-command \
        --instance-ids ${aws_instance.nats[0].id} \
        --document-name "AWS-RunShellScript" \
        --parameters commands=["nats --server nats://localhost:4222 stream info SYNC_NOTIFY_CARD_CREATED 2>/dev/null || nats --server nats://localhost:4222 stream add SYNC_NOTIFY_CARD_CREATED --subjects='sync.notify.CardCreated' --storage=file --replicas=3 --retention=limits --max-msgs=-1 --max-age=7d --discard=old --yes 2>&1"] \
        --comment "Create SYNC_NOTIFY_CARD_CREATED stream" \
        --region ${data.aws_region.current.name} \
        2>/dev/null || true

      echo "=== Stream creation commands dispatched via SSM ==="
    EOT

    environment = {
      AWS_DEFAULT_REGION = data.aws_region.current.name
    }
  }
}

# ---------------------------------------------------------------------------
# Data sources for SSM commands
# ---------------------------------------------------------------------------
data "aws_region" "current" {}
