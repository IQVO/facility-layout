---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Fleet rule: CloudEvents 1.0 on every Kafka message

- CloudEvents 1.0 is MANDATORY on every message the fleet publishes or consumes. There is no flat envelope, no dual-write, no `EVENT_ENVELOPE_MODE` toggle and no default-off flag: `TestCloudEventsOnly` (internal/architecture/events_fitness_test.go) fails the build if the toggle reappears or a module that uses Kafka stops using the CloudEvents SDK.
- `type` = `com.warehouse.<subdomain>.<context>.<entity>.<Event>`; subdomain is `wms` for facility-layout and inventory-storage, `wes` for every other context.
- Use the service's own CloudEvents helper (New / Decode / content-type header, built on the CloudEvents Go SDK) for every publish and consume; never hand-build the envelope or read raw JSON fields from the message value.
- A consumer rejects a message that is not a valid CloudEvents 1.0 envelope; it does not fall back to an older shape.
- Changing an event contract is a cross-repo change: update the AsyncAPI document, the owning ADR and every consumer in the same coordinated set of PRs, and keep the cross-service type strings identical on both sides (no repo imports another's Go code).
