{
  "family": "decisionstack-prod-sync",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "512",
  "memory": "1024",
  "executionRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-ecs-task-execution",
  "taskRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-sync-task",
  "containerDefinitions": [
    {
      "name": "sync",
      "image": "<IMAGE>",
      "essential": true,
      "portMappings": [
        {
          "containerPort": 8080,
          "protocol": "tcp"
        }
      ],
      "environment": [
        { "name": "SERVICE_NAME", "value": "sync" },
        { "name": "PORT", "value": "8080" },
        { "name": "LOG_LEVEL", "value": "info" }
      ],
      "secrets": [
        {
          "name": "DATABASE_URL",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/rds-credentials"
        },
        {
          "name": "REDIS_URL",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/redis-credentials"
        },
        {
          "name": "NATS_URL",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/nats-credentials"
        },
        {
          "name": "STRIPE_API_KEY",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/stripe-credentials"
        },
        {
          "name": "FCM_CREDENTIALS",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/fcm-credentials"
        },
        {
          "name": "APNS_CREDENTIALS",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/apns-credentials"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/decisionstack-prod/sync",
          "awslogs-region": "${AWS_REGION}",
          "awslogs-stream-prefix": "sync",
          "awslogs-create-group": "true"
        }
      },
      "healthCheck": {
        "command": ["CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"],
        "interval": 30,
        "timeout": 5,
        "retries": 3,
        "startPeriod": 60
      },
      "ulimits": [
        {
          "name": "nofile",
          "softLimit": 65536,
          "hardLimit": 65536
        }
      ],
      "dockerLabels": {
        "com.decisionstack.service": "sync",
        "com.decisionstack.managed_by": "github-actions"
      }
    }
  ]
}
