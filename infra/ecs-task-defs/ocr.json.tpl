{
  "family": "decisionstack-prod-ocr",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "512",
  "memory": "1024",
  "executionRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-ecs-task-execution",
  "taskRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-ocr-task",
  "containerDefinitions": [
    {
      "name": "ocr",
      "image": "<IMAGE>",
      "essential": true,
      "portMappings": [
        {
          "containerPort": 8081,
          "protocol": "tcp"
        }
      ],
      "environment": [
        { "name": "SERVICE_NAME", "value": "ocr" },
        { "name": "PORT", "value": "8081" },
        { "name": "LOG_LEVEL", "value": "info" },
        { "name": "PYTHONUNBUFFERED", "value": "1" }
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
          "name": "OCR_API_KEY",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/ocr-api-key"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/decisionstack-prod/ocr",
          "awslogs-region": "${AWS_REGION}",
          "awslogs-stream-prefix": "ocr",
          "awslogs-create-group": "true"
        }
      },
      "healthCheck": {
        "command": ["CMD-SHELL", "curl -f http://localhost:8081/v1/health || exit 1"],
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
        "com.decisionstack.service": "ocr",
        "com.decisionstack.managed_by": "github-actions"
      }
    }
  ]
}
