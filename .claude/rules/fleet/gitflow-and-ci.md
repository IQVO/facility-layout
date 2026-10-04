---
paths:
  - ".github/**"
  - "Makefile"
  - "lefthook.yml"
---

# Fleet rule: GitFlow, branch protection and the quality gate

- Work on `feature/*` (or `fix/*`) branches and open a PR into `develop` (`gh pr create --base develop --body-file <file>`). Never push to develop or main; main is release-only and the publish/release jobs run on main only.
- CI must be green before merge. Merging is the user's decision (an explicit word), never an agent's default. Force-push and any destructive step need the user's explicit go first.
- Local gates: lefthook runs fmt/vet/lint on commit and `make check` on push; `make check-fast` is the quick loop and `make check-all` the fuller gate. Never bypass with `--no-verify`.
- A red scheduled sensor (weekly mutation, drift, e2e) opens a `harness:red` issue; fix the code or tests, never lower a threshold in `.gremlins.yaml`, `.golangci.yml` or the architecture tests to make it pass.
- The GitHub organisation is IQVO. Delete tracked files with `git rm`, not `rm -rf`.
