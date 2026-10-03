---
paths:
  - "internal/adapters/inbound/http/**"
  - "internal/adapters/outbound/postgres/**"
  - "migrations/**"
---

# Fleet rule: idempotent writes and the transactional outbox

- Resource-creating POSTs require an `Idempotency-Key` header (the fleet idempotency middleware; the decision record lives in facility-layout): a missing key is a 400 `idempotency-key-required`. Every client in the fleet (UIs, e2e, simulators, agents) must send a fresh key per logical action.
- Events are published through the transactional outbox: the state change and the outbox row commit in one transaction and a relay publishes afterwards. Never publish to Kafka directly from a request handler.
- Test suites that run against shared infrastructure are idempotent: scope data by a per-run token and never purge tables in a setup step.
