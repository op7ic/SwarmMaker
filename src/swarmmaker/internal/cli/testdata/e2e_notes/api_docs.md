# Ticket Management API

## Endpoints

### POST /api/v1/tickets
Create a new support ticket.

Request body:
| Field       | Type   | Required | Description                    |
|-------------|--------|----------|--------------------------------|
| title       | string | yes      | Short description of the issue |
| description | string | yes      | Detailed problem description   |
| priority    | string | yes      | One of: P0, P1, P2, P3        |
| reporter    | string | yes      | Email of the reporting user    |
| tags        | array  | no       | Classification tags            |

Response: `201 Created` with ticket ID in `Location` header.

### GET /api/v1/tickets/{id}
Retrieve ticket details including status, assignee, and SLA timers.

### PATCH /api/v1/tickets/{id}
Update ticket fields. Supports partial updates.

### GET /api/v1/metrics/sla
Returns SLA compliance metrics as CSV:
```
priority,total,met_sla,breached_sla,compliance_pct
P0,42,38,4,90.5
P1,156,148,8,94.9
P2,892,871,21,97.6
P3,2341,2298,43,98.2
```

## Authentication
All endpoints require Bearer token authentication via the `Authorization` header.

## Rate Limits
- Standard: 100 requests/minute
- Bulk operations: 10 requests/minute
