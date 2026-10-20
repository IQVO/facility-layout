---
paths:
  - "**/*_test.go"
  - "internal/adapters/**/kafka/**"
---

# Fleet rule: Kafka tests and consumers

- Kafka and Postgres integration tests MUST use testcontainers. Never skip a test because `KAFKA_BROKERS` or `DATABASE_URL` / `ANALYTICS_DATABASE_URL` is unset, and never hardcode `localhost:9092`: the CI integration job provisions no service containers (no Postgres, no Kafka), so a skip-gated test passes while proving nothing (`TestKafkaIntegrationTestsUseTestcontainers` and `TestPostgresIntegrationTestsUseTestcontainers` enforce it).
- There is ONE Kafka broker platform-wide. A locally run process must never join a live cluster consumer group by accident: consumer group ids come from configuration or a named function (per-process unique for local-cache replay consumers), never an inline string literal at the call site (`TestKafkaConsumerGroupNeverHardcodedInline` enforces it).
- Event-sourced local-cache consumers must not share a consumer group across process instances (each instance must replay the whole topic), and per-process replay consumers (`GroupID: uniqueConsumerGroup()`) must set `CommitInterval`, else kafka-go commits synchronously after every message and boot replay becomes O(history) x RTT (`TestReplayConsumersSetCommitInterval` enforces it).
- A weekly cross-repo sensor goes red whenever any sibling tightens a contract: when an e2e or integration run fails, read the failing step output, not just the tail of the log.
