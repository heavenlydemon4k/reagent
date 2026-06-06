{
  "family": "decisionstack-prod-intelligence",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "1024",
  "memory": "2048",
  "executionRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-ecs-task-execution",
  "taskRoleArn": "arn:aws:iam::${AWS_ACCOUNT_ID}:role/decisionstack-prod-intelligence-task",
  "containerDefinitions": [
    {
      "name": "intelligence",
      "image": "<IMAGE>",
      "essential": true,
      "portMappings": [
        {
          "containerPort": 8080,
          "protocol": "tcp"
        }
      ],
      "environment": [
        { "name": "SERVICE_NAME", "value": "intelligence" },
        { "name": "PORT", "value": "8080" },
        { "name": "LOG_LEVEL", "value": "info" },
        { "name": "PYTHONUNBUFFERED", "value": "1" },
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
          "name": "QDRANT_URL",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/qdrant-credentials"
        },
        {
          "name": "NEO4J_URI",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/neo4j-credentials"
        },
        {
          "name": "LLM_API_KEY",
          "valueFrom": "arn:aws:secretsmanager:${AWS_REGION}:${AWS_ACCOUNT_ID}:secret:decisionstack/prod/llm-api-key"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/decisionstack-prod/intelligence",
          "awslogs-region": "${AWS_REGION}",
          "awslogs-stream-prefix": "intelligence",
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
        "com.decisionstack.service": "intelligence",
        "com.decisionstack.managed_by": "github-actions"
      }
    }
  ]
}
