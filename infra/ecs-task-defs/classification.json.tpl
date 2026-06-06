{
  "family": "decisionstack-prod-classification",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "256",
  "memory": "512",
  "executionRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-ecs-task-execution",
  "taskRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-classification-task",
  "containerDefinitions": [
    {
      "name": "classification",
      "image": "<IMAGE>",
      "essential": true,
      "portMappings": [
        {
          "containerPort": 8080,
          "protocol": "tcp"
        }
      ],
      "environment": [
        { "name": "SERVICE_NAME", "value": "classification" },
        { "name": "PORT", "value": "8080" },
        { "name": "LOG_LEVEL", "value": "info" },
        { "name": "LLM_API_ENDPOINT", "value": "https://api.openai.com/v1" }
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
          "name": "LLM_API_KEY",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/llm-api-key"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/decisionstack-prod/classification",
          "awslogs-region": "${AWS_REGION}",
          "awslogs-stream-prefix": "classification",
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
        "com.decisionstack.service": "classification",
        "com.decisionstack.managed_by": "github-actions"
      }
    }
  ]
}
