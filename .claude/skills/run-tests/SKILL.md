---
name: run-tests
description: Run devedge's Go test suites (unit, integration, e2e). Use whenever you need to run tests, run a single package or single test, add the race detector, or check that a change is green before the QA gate. Avoids rediscovering test commands and layout.
---

# Run devedge tests

devedge has three test layers (Constitution III):

| Layer | Location | Command | Needs |
|-------|----------|---------|-------|
| Unit | `pkg/...`, `internal/...` (`*_test.go`) | `make test` | — |
| Integration | `test/integration/` | `go test ./test/integration/...` | — |
| E2E (k3d) | `test/e2e/` | `go test ./test/e2e/...` | Docker + k3d |

`make test` runs `go test ./...` across everything and does **not** require Docker/k3d.

## Common commands

- Everything: `make test`
- One package: `go test ./pkg/config/...`
- One test, verbose: `go test -run TestName -v ./pkg/config/...`
- Race detector: `go test -race ./...`
- Lint (separate): `make lint` (`go vet ./...`)

## Notes

- E2E tests require Docker + k3d. If unavailable, run unit + integration and explicitly state
  that e2e was skipped — do not claim it passed.
- Treat flaky e2e tests as defects (Constitution III), not noise.
