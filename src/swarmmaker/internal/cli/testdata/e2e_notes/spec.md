# Support Platform Architecture

## System Overview

The support platform consists of three main services:

1. **Ticket Service** - CRUD operations for support tickets, SLA tracking
2. **Routing Service** - Automatic ticket classification and assignment
3. **Analytics Service** - Reporting, dashboards, SLA compliance metrics

## Data Flow

```
User -> API Gateway -> Ticket Service -> PostgreSQL
                    -> Routing Service -> Redis (queue)
                    -> Analytics Service -> ClickHouse
```

## Technology Stack
- Backend: Go 1.22
- Database: PostgreSQL 16 (tickets), ClickHouse (analytics)
- Queue: Redis Streams
- API: REST with OpenAPI 3.0 spec
- Auth: OAuth2 with JWT tokens

## Deployment
- Kubernetes on AWS EKS
- Helm charts for service deployment
- ArgoCD for GitOps
- Datadog for observability
