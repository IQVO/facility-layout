---
id: 0047-concurrent-creation-race
slug: /adr/0047-concurrent-creation-race
title: "47. Exactly one concurrent creation of the same code wins; the loser gets the duplicate refusal"
sidebar_label: "47. Concurrent creation race"
sidebar_position: 47
description: "ADR 0047 — fixes the creation race ADR 0046 left as a follow-up. A creation write never overwrites a committed row: the versioned repos refuse a version-0 (never-persisted) aggregate's guarded upsert, the non-versioned creation repos get a narrow Create (INSERT ... ON CONFLICT DO NOTHING), and every register use case maps the lost race to its FINAL duplicate-* refusal. The natural keys already carry PK/UNIQUE indexes; no migration."
---

# 47. Exactly one concurrent creation of the same code wins; the loser gets the duplicate refusal

## Status

Accepted (2026-11-20). Fixes the follow-up [ADR 0046](./0046-idempotency-replay-classes.md) named ("A creation
race is not guarded"); its Decision text is untouched and its follow-up bullet carries a dated pointer here.

## Context

ADR 0046 staged a lost race to prove the RETRYABLE class and found a second defect in the process: every
creation path checked existence (`FindByCode`/`FindByID`/`FindByName`) BEFORE opening its write, so two
requests registering the SAME new code both passed the check, and the write itself was an upsert that
overwrote:

- `sites`, `location_slots` (version-guarded since ADR-0025/0035): a NEW aggregate carried version 1 — the
  same number the `INSERT` arm writes — so the second writer's `ON CONFLICT DO UPDATE ... WHERE version = 1`
  matched the first writer's fresh row (also version 1), applied, and answered 201. The first request's data
  was silently replaced.
- `zones`, `aisles`, `cross_aisles`, `fixed_structures`, `location_types`, `placement_rules`: `Save` was a
  blind `ON CONFLICT DO UPDATE` with no guard at all — the same overwrite, with or without concurrency
  (the existence check was the only protection, and it races).

Both requests answered 201; the loser's payload replaced the winner's row. Reproduced red through the real
router and Postgres on all eight keyed creation routes (PR #289's first commit; CI run 44753803654,
`integration` job: `loser = 201` eight times).

GLN claims were never exposed: `location_glns` is claimed with `INSERT ... ON CONFLICT (gln) DO NOTHING`
plus an owner re-read (`gln_registry.go`), so the loser of two concurrent claims already got the FINAL
`gln-already-assigned` (ADR 0045). Nothing to change there.

Every natural key at stake already has a total unique index — `sites.code`, `location_slots.code`,
`location_types.name` (PKs), `zones` PK id + `UNIQUE (site_code, area_code, zone_code)`, `aisles` PK id +
`UNIQUE (zone_id, aisle_code)`, `cross_aisles (zone_id, from_aisle, to_aisle, at_bay)`,
`fixed_structures.id`, `placement_rules.id` — and zone/aisle ids are derived deterministically from the
caller-chosen segments, so the id and the natural triple are the same conflict. No new index, no migration.

## Decision

**A creation write never overwrites a committed row. Exactly one creation of a natural key wins; the loser
is answered with the documented per-entity duplicate refusal — a FINAL problem type under ADR 0046, stored
and replayed under the loser's own Idempotency-Key.** One repository sentinel expresses the lost race and
every register use case maps it to the duplicate error it already returns sequentially:

1. **`ports.ErrNaturalKeyTaken`** is the sentinel a repository returns when the insert of a NEW aggregate
   found the natural key held by a stored (committed) row.
2. **Versioned aggregates (`sites`, `location_slots`)** — ADR 0046's own suggested mechanism: a NEW
   aggregate now carries **version 0** ("never persisted"); the repository writes the first stored row at
   version 1 as before. The SQL of the guarded upsert is unchanged, because the `WHERE version = $loaded`
   arm can never match a version-0 save against a stored row (stored rows are >= 1): 0 rows affected for a
   version-0 save can only mean the key is taken, and `Save` answers the sentinel; a version >= 1 save
   keeps its exact ADR-0025 `ErrConcurrentModification` semantics. `Save` therefore can never create over
   an existing row again, whatever a future use case does.
3. **Non-versioned creation repos (`zones`, `aisles`, `cross_aisles`, `fixed_structures`,
   `location_types`, `placement_rules`)** get a narrow **`Create`** port method:
   `INSERT ... ON CONFLICT <natural key> DO NOTHING`, 0 rows => the sentinel. `Save` keeps today's upsert
   and remains the update path (`SetAisleGeometry` writes a rehydrated aisle; the test seeding decommissions
   parents through `Save`). The aisle needs both paths, which is why the discriminator lives in the port
   there rather than in a version the aisle does not have.
4. **Every register use case** (`RegisterSite`, `RegisterZone`, `RegisterAisle`, `RegisterCrossAisle`,
   `RegisterFixedStructure`, `RegisterLocationType`, `DefinePlacementRule`, `RegisterLocationSlot`) maps the
   sentinel to its duplicate error via one helper (`lostCreationRace`), so the HTTP answer is the same
   `duplicate-*` problem the pre-write existence check already produces — status, slug and FINAL class
   unchanged, no wire change, no new problem type. The loser's refusal commits WITH its key row (FINAL) and
   replays byte-identically; the loser's outbox event and any GLN claim roll back with its transaction.
5. **The bulk import ensures, it does not fight**: `ensureSite`/`ensureZone`/`ensureAisle` create through
   the creation paths above, and when a parent's creation loses its race they re-read it and ADOPT it (the
   Active check still applies) — the import's goal is only that the parent exists. A raced SLOT still
   reports its row as rejected under the existing duplicate semantics of the row report.

The concurrent-mechanics claim of ADR 0046 is unchanged: the loser blocks on the winner's uncommitted index
entry until the winner commits, then sees the conflict — the difference is that the conflict now refuses
instead of overwriting.

## Consequences

- One 201, one FINAL 409 `duplicate-*`, exactly one row carrying the winner's payload — proven per route by
  `creation_race_integration_test.go` (staged: the winner is parked after its write inside its open
  transaction while the loser blocks on the row), which was pushed alone first and was red on develop
  (run 44753803654) before this fix.
- The memory test doubles mirror `Create`'s refusal but keep `Save` as a plain upsert: they hold no row
  versions of their own, so the version-0 discrimination is Postgres's (the concurrent contract is the
  integration suite's; sequential flows stay correct through the existence checks).
- A site or slot read back after registration rehydrates at version 1 exactly as before; a fresh aggregate
  reports version 0, which no response body or event ever carried.
- Registering over a decommissioned code is still refused (`duplicate-*`, FINAL — a retired code is never
  resurrected, ADR 0005): the sentinel makes no distinction the existence check did not make.
- Follow-up, not in this change: the non-versioned `Save` upserts (`zones`, structures, types, rules,
  cross-aisles) have no version guard for genuine updates; no update use case exists for them today, and
  one that arrives must bring its own concurrency story.

## Tests

- `internal/adapters/inbound/http/creation_race_integration_test.go` (Postgres, testcontainers, the real
  router): one staged race per keyed creation route — one 201, one 409 `duplicate-*`, one row carrying the
  winner's data, the loser's key row stored, and the loser's replay answering the stored refusal with
  `Idempotency-Replayed: true`. Red on develop (CI run 44753803654, `integration` job), green with this ADR.
- `internal/application/usecases/creation_race_test.go` (Docker-free): every register use case maps
  `ports.ErrNaturalKeyTaken` to its duplicate refusal (racing repo doubles); the import adopts a parent
  whose creation lost the race.
- `internal/domain`: a fresh Site/LocationSlot carries version 0.
- `internal/adapters/outbound/memory/creation_test.go`: `Create` refuses a held natural key; `Save` keeps
  the upsert.
