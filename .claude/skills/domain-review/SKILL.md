---
name: domain-review
description: Ubiquitous-language drift review of a change against .claude/rules/domain-model.md: renamed or invented terms, aggregate-invariant leaks. Invoke explicitly: /domain-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a ubiquitous-language drift review of the current changes (or
`$ARGUMENTS` if given), comparing new/changed code against
`.claude/rules/domain-model.md` (or this repo's equivalent doc — check
`AGENTS.md`/`CLAUDE.md` for where the ubiquitous language lives if that
file doesn't exist here).

Ubiquitous language drift is the quiet failure mode DDD is supposed to
prevent: code that technically works but silently renames, reshapes, or
duplicates a concept the domain-model doc already named — so future
readers can no longer map code to domain conversation.

## What to check, in priority order

1. **A new type/field/method that duplicates an existing domain concept
   under a different name.** If the domain-model doc already names a
   concept, a new calculation that computes the same thing under a
   different name is drift — even if the math is correct, it fragments
   the vocabulary. Flag it and point at the existing name.
2. **A domain type/method named in implementation terms instead of
   domain terms.** `internal/domain/` code should read like the ubiquitous
   language, not like database/HTTP vocabulary — a method called
   `UpdateRow` or `PatchState` where the domain-model doc would call the
   equivalent operation `Stow`/`Revoke`/`RunCycleCount` is drift, and the
   fitness-test suite won't catch this because it's a naming problem, not
   an import-direction problem.
3. **An invariant enforced in code that the domain-model doc doesn't
   mention, or vice versa.** If a new domain rule was added to the code
   (a new validation, a new state-transition guard), the domain-model doc
   should be updated in the SAME PR — an undocumented invariant is
   invisible to the next person who touches that aggregate and may
   accidentally remove it thinking it's dead code.
4. **A value object that should be closed but was implemented open (or
   vice versa).** A categorical field that carries real regulatory/
   physical meaning usually wants a deliberately closed enum, while an
   open extensible tag set is right for something genuinely open-ended —
   check any new categorical field against which the domain actually
   wants, and flag a mismatch either direction.
5. **A cross-aggregate rule implemented as a cross-aggregate call instead
   of an explicit local check, or vice versa**, per whatever this repo's
   own domain-model doc says about which invariants are local vs. which
   legitimately need external state (e.g. this repo's DOT segregation
   check is explicitly documented as "purely LOCAL... no cross-context
   call" — a change that quietly makes it call out to another service
   would be a real regression worth flagging even if functionally it
   still "works").
6. **New terminology introduced without updating the domain-model doc.**
   If new code introduces a genuinely new domain concept the doc doesn't
   yet name, that's not necessarily wrong — but the doc needs a new entry
   in the SAME PR, or the vocabulary silently forks between prose and
   code.

## Output format

For each finding: the term/concept involved, where it appears in the
domain-model doc (or "not yet documented"), where the drift appears in
code, and a one-sentence recommendation (rename, document, or confirm
it's an intentional new concept). If the changes introduce no new domain
concepts and use existing vocabulary correctly, say so plainly.

This command never modifies files. It is advisory input for the author
to act on, not a blocking gate.
