# MASS Automation Runner Reliability Plan

Status: planned
Date: 2026-06-06
Repository: github.com/MiviaLabs/go-mivia
Jira: not checked by repo constraint
Confluence: not checked by repo constraint
Internet: not used; current failures are fully local to Mivia source, tests, runner logs, and REST/MCP state.

## Goal

Make external automation runners reliable enough to run MASS workers at scale without permanent claim loops, stale running runs, resume-instruction validation deadlocks, or blind recovery churn.

This is not a narrow patch plan. Treat the current fixes as untrusted until the regression suite below proves the whole lifecycle.

## Current Source Evidence

- Work Task resume instructions are still length-validated in service code:
  - `internal/projectworkplan/service.go:19-21` defines `MaxResumeInstructionsLength = 16 * 1024`.
  - `internal/projectworkplan/service.go:311` validates task creation resume instructions with that max.
  - `internal/projectworkplan/service.go:625-628` validates transition-provided resume instructions with that max.
  - `internal/projectworkplan/service.go:1045-1054` rejects text longer than max and rejects unsafe/PII-like content.
- MCP Work Task schemas still expose a stricter 1200-byte max:
  - `internal/projectworkplan/mcpapi/mcpapi.go:51-55` defines `longText` and `optionalText` with `maxLength: 1200`.
  - `internal/projectworkplan/mcpapi/mcpapi.go:74-91` uses those schemas for `resume_instructions` in create/update/block tools.
- Automation claim/recovery is spread across many code paths:
  - `internal/projectautomation/service.go:775-885` runs reconciliation, recovery claims, ready automation reconciliation, and queued run claim.
  - `internal/projectautomation/service.go:888-936` reclaims failed or blocked pre-execution failures as `pre_execution_recovery`.
  - `internal/projectautomation/service.go:950-997` reconciles pre-execution runs that progressed task state.
  - `internal/projectautomation/service.go:1263-1380` records `CompleteAttempt`.
  - `internal/projectautomation/service.go:1366-1368` immediately reroutes only GitOps post-task recovery failures.
  - `internal/projectautomation/service.go:1525-1577` can reroute failed pre-execution recovery, but it is only reached later from exhaustion reconciliation.
  - `internal/projectautomation/service.go:1596-1665` reconciles running runs.
  - `internal/projectautomation/service.go:1712-1720` considers a running run abandoned only when it started before current server process start.
  - `internal/projectautomation/service.go:1723-1765` requeues abandoned running runs.
  - `internal/projectautomation/service.go:2128-2168` queues ready dependent automation.
  - `internal/projectautomation/service.go:2171-2210` applies replacement retry limit and blocks the task.
  - `internal/projectautomation/service.go:2213-2265` counts terminal replacement failures.
  - `internal/projectautomation/service.go:2551-2629` prepares queued runs for execution.
  - `internal/projectautomation/service.go:3217-3224` treats dirty-worktree and start/claim failures as recoverable pre-execution failures.
- Store update behavior is a partial race guard, not a full state-machine contract:
  - `internal/projectautomation/store/store.go:12-21` preserves existing running recovery claims for selected safe summaries.
  - `internal/projectautomation/store/memory.go:127-138` and `internal/projectautomation/store/ladybug.go:174-198` both call that preserve helper.
- Runner behavior has no lease, heartbeat, or durable completion confirmation:
  - `cmd/mivia-automation-runner/main.go:72` configures a plain HTTP client timeout.
  - `cmd/mivia-automation-runner/main.go:198-329` claims, executes, reports one result, and logs the server-returned status.
  - `cmd/mivia-automation-runner/main.go:811-814` posts `CompleteAttempt` to REST.
  - There is no runner heartbeat route, no claim token in `CompleteAttemptInput`, and no post-report GET verification.
- Local compose config shows the live runner shape:
  - `.docker-compose.local.yml:75-89` runs `mivia-automation-runner-mass` in watch mode for `mass-monorepo`.
  - `.docker-compose.local.yml:80-82` uses poll interval `5s` and request timeout `60s`.

## Confirmed Failure Modes To Cover

1. `resume_instructions` length mismatch can still reject MCP Work Task updates before service code sees them, because MCP schema max is 1200 while service max is 16 KiB.
2. Stored long resume instructions must never break claim/recovery/status transitions when the transition does not replace resume instructions.
3. Failed pre-execution recovery is retried through recovery claim loops until retry exhaustion instead of being rerouted deterministically at completion time.
4. Running runs have no heartbeat/lease. A runner can die or finish without a durable terminal transition during the same server uptime, and the server has no proof whether the work is alive.
5. The store uses ad hoc preservation for running recovery claims instead of explicit compare-and-set transition rules.
6. The runner logs the status returned by `CompleteAttempt`, but it does not confirm the durable run read after the report.
7. Replacement retry limiting and explicit operator reruns need one invariant: explicit operator reruns must survive reconciliation, while automatic replacements must stop after the cap.

## Non-Negotiable Invariants

Every implementation step must preserve these:

1. A Work Task can have at most one active run that owns the task.
2. Every active external run has a claim token, runner id, claimed timestamp, last heartbeat timestamp, and lease expiry.
3. `CompleteAttempt` must be idempotent for the same claim token and must reject stale/different claim tokens.
4. A terminal `CompleteAttempt` response must match a durable `GetRun` read immediately after the write.
5. Same-server stale `running` runs must expire after lease timeout, not only after server restart.
6. Pre-execution recovery must not blindly retry deterministic dirty-worktree/scope failures.
7. Automatic replacement runs are capped per task; explicit operator reruns are not counted as automatic replacements.
8. Resume instructions have no field-level schema max. They still pass unsafe-marker and PII-like checks.
9. No logs, fixtures, API responses, or plan files may include raw prompts, source dumps, raw stderr, roots, secrets, tokens, or PII.

## Implementation Tasks For Agents

### Task 1: Add Failing Regression Tests First

Files:
- `internal/projectautomation/service_test.go`
- `internal/projectautomation/store/ladybug_test.go`
- `internal/projectautomation/httpapi/httpapi_test.go`
- `cmd/mivia-automation-runner/main_test.go`
- `internal/projectworkplan/service_test.go`
- `internal/projectworkplan/mcpapi/mcpapi_test.go`
- `internal/projectworkflow/validation_test.go`

Required tests:

1. `TestMCPWorkTaskSchemaDoesNotCapResumeInstructionsAt1200`
   - Assert `projects.work_tasks.create`, `projects.work_tasks.update_status`, and `projects.work_tasks.block` schemas do not put `maxLength: 1200` on `resume_instructions`.

2. `TestWorkTaskAllowsLongResumeInstructionsWithoutBlockingLifecycle`
   - Create a task with resume instructions longer than 16 KiB.
   - Transition it through `ready -> claimed -> in_progress -> blocked` with long resume instructions.
   - Assert unsafe markers and PII-like values are still rejected.

3. `TestAutomationClaimIgnoresStoredLongResumeInstructions`
   - Create a ready task that already has very long resume instructions.
   - Queue an automation run.
   - Claim it.
   - Assert claim succeeds and does not rewrite or revalidate stored resume text unless input supplies new resume text.

4. `TestCompleteAttemptFailedPreExecutionRecoveryReroutesWithoutBlindRetry`
   - Start with a run in `running` with `SafeSummary == "pre_execution_recovery"`.
   - Complete with `Status=failed` and `FailureCategory=gitops_dirty_worktree_scope`.
   - Assert the same run is terminal failed with `pre_execution_recovery_failed_requires_implementation`.
   - Assert task is ready or blocked according to deterministic classifier.
   - Assert next claim does not reclaim the same failed run as `pre_execution_recovery`.

5. `TestClaimNextRunExpiresSameServerStaleRunningRunAfterLease`
   - Create a running external run with stale `LeaseExpiresAt`, stale `LastHeartbeatAt`, and task `in_progress`.
   - Call `ClaimNextRun` during the same server uptime.
   - Assert old run becomes `timeout` with `external_runner_interrupted`.
   - Assert task is released to `ready`.
   - Assert a replacement automatic run is queued only when allowed and under cap.

6. `TestClaimNextRunDoesNotExpireFreshHeartbeat`
   - Same setup as above, but heartbeat is fresh and lease has not expired.
   - Assert `ClaimNextRun` leaves it active and does not queue duplicate work.

7. `TestCompleteAttemptRequiresMatchingClaimToken`
   - Claim a run and capture `claim_id`.
   - Attempt completion with missing or wrong `claim_id`.
   - Assert rejected without attempt write or state change.
   - Complete with correct `claim_id`.
   - Assert attempt written and durable run terminal/verifying as expected.

8. `TestCompleteAttemptDuplicateTerminalReportIsIdempotent`
   - Complete the same claimed run twice with the same claim token and same terminal status.
   - Assert no duplicate attempt write and returned run is the existing terminal run.

9. `TestConcurrentRecoveryClaimsOnlyOneLease`
   - Use multiple goroutines claiming the same recoverable run.
   - Assert exactly one receives a claim with a unique `claim_id`.
   - Assert other callers get no work or a different queued run, never the same run.

10. `TestRunnerHeartbeatsUntilCompletion`
    - Fake server claims one run, accepts heartbeat calls, then accepts attempt result.
    - Assert runner sends at least one heartbeat during long fake Codex execution.
    - Assert completion includes `claim_id` and runner id.

11. `TestRunnerVerifiesDurableCompletionBeforeReportedLog`
    - Fake server returns failed from attempt-result but GET still returns running.
    - Assert runner treats this as report failure or logs an explicit durable-state mismatch, not `reported failed`.

12. `TestExplicitOperatorRunSurvivesReadyReconcileAfterReplacementLimit`
    - Keep the existing explicit-rerun behavior test and extend it with lease fields.
    - Assert an active explicit operator run blocks automatic replacement queueing for the same task.

Run this first after adding tests:

```sh
/home/mac/.local/bin/go test ./internal/projectautomation ./internal/projectworkplan ./cmd/mivia-automation-runner
```

Expected before fixes: at least the new tests fail for the current behavior.

### Task 2: Add External Runner Claim Lease Model

Files:
- `internal/projectautomation/model.go`
- `internal/projectautomation/store/memory.go`
- `internal/projectautomation/store/ladybug.go`
- `internal/projectautomation/store/store.go`
- `internal/projectautomation/service.go`

Add fields to `AutomationRun`:

```go
ClaimID          string    `json:"claim_id,omitempty"`
RunnerID         string    `json:"runner_id,omitempty"`
ClaimedAt        time.Time `json:"claimed_at,omitempty"`
LastHeartbeatAt  time.Time `json:"last_heartbeat_at,omitempty"`
LeaseExpiresAt   time.Time `json:"lease_expires_at,omitempty"`
```

Rules:

- Claiming any external run creates a new `ClaimID`.
- `ClaimID` changes only when a new live claim is granted.
- `RunnerID` is a safe ref supplied by the runner or generated by server when missing.
- `LastHeartbeatAt` and `LeaseExpiresAt` are set at claim time.
- Terminal states clear no ownership fields except where existing API compatibility requires keeping history.
- Persist all fields in memory and Ladybug stores.

Replace `shouldPreserveExistingRun` with explicit transition checks:

- Add a store-level conditional update method, or a service-level helper that re-reads current run and validates expected status, claim id, attempt count, and safe summary before write.
- Do not preserve running claims by safe summary alone.
- A stale writer must return the current run and a typed stale-transition error; it must not silently discard a terminal update.

### Task 3: Add Heartbeat API

Files:
- `internal/projectautomation/model.go`
- `internal/projectautomation/service.go`
- `internal/projectautomation/httpapi/httpapi.go`
- `internal/projectautomation/httpapi/httpapi_test.go`
- `internal/projectautomation/mcp_adapter.go` only if MCP should expose heartbeat later; REST is enough for runner.

Add input:

```go
type HeartbeatRunInput struct {
    ProjectID string `json:"project_id,omitempty"`
    RunID     string `json:"run_id"`
    ClaimID   string `json:"claim_id"`
    RunnerID  string `json:"runner_id,omitempty"`
}
```

Add REST:

```text
POST /api/v1/projects/{id}/automation-runs/{run_id}/heartbeat
```

Behavior:

- Reject if run is not active.
- Reject if `claim_id` does not match.
- Update `LastHeartbeatAt`, `LeaseExpiresAt`, and `UpdatedAt`.
- Do not alter Work Task status.
- Return current run.

### Task 4: Refactor Claim And Recovery State Machine

Files:
- `internal/projectautomation/service.go`
- `internal/projectautomation/service_test.go`

Refactor into named helpers:

- `claimQueuedExternalRun`
- `claimPreExecutionRecoveryRun`
- `claimGitOpsRecoveryRun`
- `completeExternalAttempt`
- `expireStaleExternalRun`
- `rerouteFailedPreExecutionRecovery`
- `rerouteFailedGitOpsRecovery`

Required behavior:

- `ClaimNextRun` first expires stale active runs based on `LeaseExpiresAt`, not `runStartedBeforeService`.
- Fresh active runs remain active and prevent duplicate work.
- Recovery claim helpers must set claim lease fields.
- Failed pre-execution recovery must not blindly re-enter `claimPreExecutionRecovery`.
- Dirty-worktree/scope categories must reroute to implementation or block immediately.
- Transient categories may retry only under an explicit retry classifier and must still respect max retries.
- `requeueTaskAfterPreExecutionRecoveryFailure` must be called from `CompleteAttempt` for failed `pre_execution_recovery` when the category is deterministic or retries are exhausted.
- `queueReadyDependentAutomation` must continue to preserve explicit operator runs and cap automatic replacements.

### Task 5: Make CompleteAttempt Claim-Safe And Durable

Files:
- `internal/projectautomation/model.go`
- `internal/projectautomation/service.go`
- `internal/projectautomation/httpapi/httpapi.go`
- `internal/projectautomation/mcp_adapter.go`
- `cmd/mivia-automation-runner/main.go`

Changes:

- Add `ClaimID` and `RunnerID` to `CompleteAttemptInput`.
- For active runs, require matching claim id.
- For terminal duplicate reports, return the existing terminal run if the same claim id already completed.
- Write attempt and run terminal transition under one serialized service section.
- After `UpdateRun`, re-read run from store and compare:
  - status
  - failure category
  - claim id
  - finished timestamp is non-zero for terminal states
- Return an error if durable read does not match the intended terminal/verifying state.

Do not allow a response saying `failed`, `completed`, `timeout`, or `verifying` while `GetRun` still returns `running`.

### Task 6: Add Runner Heartbeat And Report Verification

Files:
- `cmd/mivia-automation-runner/main.go`
- `cmd/mivia-automation-runner/main_test.go`
- `docker-compose.yml`
- `.docker-compose.local.yml`
- `configs/mivia-server.example.toml`
- `configs/mivia-server.local.toml`

Runner changes:

- Generate stable runner id at process start, for example `hostname:pid`.
- After claim, start a heartbeat goroutine using returned `claim_id`.
- Heartbeat interval must be shorter than lease TTL. Suggested defaults:
  - heartbeat interval: `15s`
  - lease TTL: `90s`
  - stale grace: `30s`
- Stop heartbeat only after terminal report succeeds or the runner exits.
- Include `claim_id` and runner id in completion reports.
- After `completeAttempt`, call GET run and confirm durable state matches returned state.
- Change stdout:
  - OK: `automation run <id> durably reported <status>`
  - mismatch: `automation run <id> report mismatch returned=<status> durable=<status>`
- On report timeout/network error, retry a bounded number of times before giving up.
- If retry gives up, leave server to expire the lease.

Compose/config changes:

- Add env/options for heartbeat interval and lease TTL.
- Keep local MASS runner poll interval at 5s unless tests show claim pressure requires change.
- Do not restart compose until all tests pass.

### Task 7: Remove Resume Instruction Field-Level Max

Files:
- `internal/projectworkplan/service.go`
- `internal/projectworkplan/mcpapi/mcpapi.go`
- `internal/projectworkplan/mcp_adapter.go` if adapter has hidden caps
- `internal/projectworkflow/validation.go`
- `internal/projectworkplan/service_test.go`
- `internal/projectworkplan/mcpapi/mcpapi_test.go`
- `internal/projectworkflow/validation_test.go`
- `api/mcp/agent-control.v1.md`
- `api/openapi/agent-control.v1.yaml` if generated/maintained manually in this repo

Required behavior:

- Remove field-level max for `resume_instructions`.
- Keep unsafe marker and PII-like checks.
- Keep global HTTP request-size/storage limits if already present; do not add a smaller resume-specific max.
- Ensure old stored long values can be read and passed through transitions.
- Ensure long resume instructions are not copied into logs.

Implementation hint:

- Replace `MaxResumeInstructionsLength` use with a resume-specific sanitizer:

```go
func safeResumeInstructions(value string, name string) (string, error)
```

- This sanitizer should trim whitespace and run unsafe/PII checks, but not check length.
- Do not use this sanitizer for other text fields.

### Task 8: GitOps Dirty Worktree Classification

Files:
- `cmd/mivia-automation-runner/main.go`
- `internal/projectgitops/*`
- `cmd/mivia-automation-runner/main_test.go`
- `internal/projectautomation/service_test.go`

Required behavior:

- Pre-task GitOps failures must report enough safe category detail for the server to choose:
  - same-run transient retry
  - implementation rerun
  - operator block
- Dirty files inside `files_to_edit` should route to implementation continuation, not blind clean/reset.
- Dirty files outside `files_to_edit` should block with safe metadata and require operator inspection.
- Generated artifact paths declared in config should be treated as in scope.
- Root-owned/dubious git metadata must be classified separately from task dirtiness.

Do not reset worktrees automatically.

### Task 9: Observability And Stats

Files:
- `internal/projectautomation/httpapi/httpapi.go`
- `internal/projectautomation/mcpapi/mcpapi.go`
- `internal/dashboard/httpapi/assets/app.js` only if dashboard renders run details

Add safe metadata to run list/detail:

- `claim_id`
- `runner_id`
- `claimed_at`
- `last_heartbeat_at`
- `lease_expires_at`
- `stale_reason` when reconciled

Add counters where the current list endpoint or stats endpoint already fits:

- active runs with fresh heartbeat
- active runs with expired lease
- runs expired this server uptime
- replacement runs blocked by retry limit
- explicit operator runs active

Do not expose raw command output, paths outside project-relative safe refs, tokens, provider payloads, or raw source.

## Execution Order

1. Keep Docker compose down.
2. Confirm clean repo:

```sh
git status --short
```

3. Add tests from Task 1.
4. Run focused tests and confirm new tests fail for the intended reason:

```sh
/home/mac/.local/bin/go test ./internal/projectautomation ./internal/projectworkplan ./cmd/mivia-automation-runner
```

5. Implement Tasks 2-7.
6. Run focused tests again:

```sh
/home/mac/.local/bin/go test ./internal/projectautomation ./internal/projectworkplan ./cmd/mivia-automation-runner
```

7. Implement Task 8 only after state machine and resume length tests are green.
8. Run broader package tests:

```sh
/home/mac/.local/bin/go test ./internal/projectautomation/store ./internal/projectautomation/httpapi ./internal/projectworkplan/mcpapi ./internal/projectworkflow ./cmd/mivia-automation-runner
/home/mac/.local/bin/go test ./...
git diff --check
```

9. Build/restart only after tests pass:

```sh
docker compose -f docker-compose.yml -f .docker-compose.local.yml up -d --build --force-recreate mivia-server
docker compose -f docker-compose.yml -f .docker-compose.local.yml up -d --no-recreate --scale mivia-automation-runner-mass=12 mivia-automation-runner-mass
```

10. Verify live state:

```sh
curl -fsS http://127.0.0.1:8080/readyz
docker compose -f docker-compose.yml -f .docker-compose.local.yml logs --since=2m mivia-automation-runner-mass
```

Then use local MCP/REST only to inspect automation run and Work Task metadata.

## Acceptance Criteria

The fix is not complete until all are true:

1. No `resume_instructions is too long` error can be produced by Work Task create/update/block for safe long resume text.
2. MCP schemas do not advertise a smaller resume-instructions max than service behavior.
3. A runner that dies during the same server uptime is expired by lease and does not hold a task forever.
4. A live runner with fresh heartbeat is never expired.
5. A failed pre-execution recovery does not repeatedly reclaim the same run for deterministic dirty-worktree/scope failures.
6. A terminal `CompleteAttempt` response is durable; immediate GET cannot show `running`.
7. Duplicate/stale completion reports are idempotent or explicitly rejected by claim id.
8. Automatic replacements stop at the configured cap.
9. Explicit operator reruns remain claimable and are not blocked by automatic replacement cap.
10. MASS runner logs use `durably reported` only after durable state confirmation.
11. Focused and full Go tests pass.
12. `git diff --check` passes.

## Do Not Do

- Do not use Jira or Confluence.
- Do not start Docker compose before tests pass.
- Do not bulk reset MASS tasks.
- Do not reset or clean MASS worktrees automatically.
- Do not add raw logs, raw prompts, source dumps, roots, secrets, tokens, or PII to tests or logs.
- Do not implement another isolated one-line recovery patch without the failing regression tests above.

## Hand-Off Prompt For Implementation Agent

You are implementing `.ai/tasks/active/mass-automation-runner-reliability-plan.md`.

Rules:

1. Keep Docker compose down until tests pass.
2. Add the regression tests first.
3. Run the focused test command and confirm new failures are for the planned behavior.
4. Implement claim lease, heartbeat, claim-safe completion, immediate pre-execution recovery reroute, and resume-instruction max removal.
5. Do not use Jira or Confluence.
6. Do not bulk reset tasks or clean worktrees.
7. Run:

```sh
/home/mac/.local/bin/go test ./internal/projectautomation ./internal/projectworkplan ./cmd/mivia-automation-runner
/home/mac/.local/bin/go test ./internal/projectautomation/store ./internal/projectautomation/httpapi ./internal/projectworkplan/mcpapi ./internal/projectworkflow ./cmd/mivia-automation-runner
/home/mac/.local/bin/go test ./...
git diff --check
```

8. Only after green tests, rebuild/restart compose and verify live runner status.

