# Support Ops Workflow Notes

## Ticket Triage Process

Incoming tickets flow through three stages: classification, assignment, and resolution tracking.

### Classification Rules
- P0 (critical): service outage affecting >10% of users
- P1 (high): partial service degradation, data integrity risk
- P2 (medium): feature malfunction, workaround available
- P3 (low): cosmetic, documentation, minor UX issues

### Assignment Policy
Tickets are assigned based on the on-call rotation schedule maintained in PagerDuty.
Each engineer handles up to 5 concurrent P2/P3 tickets or 2 P0/P1 tickets.

## Escalation Procedures

If a P0 ticket is not acknowledged within 15 minutes:
1. Page the secondary on-call
2. Notify the engineering manager via Slack #incidents
3. After 30 minutes, trigger the incident commander protocol

## SLA Commitments
- P0: 15min response, 4hr resolution
- P1: 1hr response, 8hr resolution
- P2: 4hr response, 48hr resolution
- P3: 24hr response, 5 business day resolution
