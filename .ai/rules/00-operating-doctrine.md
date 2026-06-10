# Operating Doctrine

Default posture:

- Be evidence-driven. Verify claims against files, commands, tests, or runtime output.
- Keep changes small, reviewable, and aligned with the current phase.
- Do not import conventions from unrelated repositories unless this repo explicitly adopts them.
- Preserve `.ai/` as the canonical workflow source.
- Keep root and vendor adapter files as pointers to `.ai/`.

Before editing:

- Read the current task, relevant `.ai/` rules, and affected files.
- Identify whether the change touches auth, authorization, tenancy, PII, secrets, migrations, public APIs, background jobs, observability, or external integrations.
- Stop and ask for owner confirmation when missing information changes security, privacy, cost, production behavior, or irreversible data impact.

Bug-fix discipline:

- Do not start from a guessed fix. First confirm the defect against current source, tests, logs, runtime output, or a reproducible failing path.
- State the confirmed failure mode before editing: expected behavior, actual behavior, affected code path, and evidence used.
- If the bug cannot be confirmed, stop and report `Not confirmed` with the exact evidence checked and the missing evidence needed. Do not implement a speculative fix.
- Start with the narrowest regression test that should fail for the confirmed bug. Add it before or alongside the fix, and verify it fails for the right reason when practical.
- If a regression test is not feasible, record the concrete reason and use the narrowest reproducible verifier instead. `No time`, `seems obvious`, or `covered indirectly` are not acceptable reasons.
- Keep fixes limited to the confirmed failing path. Do not refactor adjacent code, broaden architecture, or add new behavior unless the evidence shows it is required.
- After the fix, rerun the new regression test first, then the smallest relevant package or integration verifier.
- For regressions, compare against the previous working behavior or contract before declaring the fix complete.

Implementation boundaries:

- Follow the approved phase scope.
- Do not create later-phase files early.
- Do not mix infrastructure, service logic, and data-model work before the phase requires it.
- Use durable files for decisions, handoffs, and task status.

Commit policy:

- Use Conventional Commits for all new commits in this repository.
- Format: `<type>(<optional-scope>): <imperative summary>`.
- Allowed common types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`, `revert`.
- Keep the subject concise and aligned with the actual change; do not hide behavioral changes under `chore`.
- Use `BREAKING CHANGE:` in the commit body when compatibility is intentionally broken.
- Preserve existing commit messages during history-rewrite or identity-only repair unless the user explicitly asks to rewrite messages.

Pull request policy:

- Use Conventional Commit style for PR titles too: `<type>(<optional-scope>): <imperative summary>`.
- Keep PR titles short and aligned with the branch/task outcome.
- Keep PR descriptions short and evidence-based.
- Every PR description must include exactly these sections: `What changed`, `How verified`, `Tests`.
- `What changed` must summarize the user-visible or operational change, not paste raw diffs.
- `How verified` must name the evidence source, reviewer/verifier refs, or runtime check used.
- `Tests` must list commands run and results, or say `Not run` with the exact reason.
- Do not include raw prompts, source dumps, raw stderr, secrets, credentials, provider payloads, roots, or PII in PR titles or descriptions.

Branch policy:

- Create new repository branches with the `mivia/` prefix.
- Use short, descriptive suffixes after the prefix, for example `mivia/workflow-runner-heartbeats` or `mivia/v0.2.3-docs-release`.
- Do not use generic agent prefixes such as `codex/`, `claude/`, `agent/`, or personal-name prefixes for this repository unless the user explicitly asks for a one-off exception.
- Keep automation-created task branches under the same `mivia/` prefix so GitOps, PRs, and worktree cleanup use one branch namespace.

Automation ordering:

- Automatic Work Task runs are triggered by lifecycle transitions, not by chat intent.
- For every task-triggered automatic automation, create the Work Task as `planned`, create and enable the matching automation, then transition the Work Task to `ready`.
- Do not create a Work Task directly as `ready` before its matching enabled automation exists. That can miss the transition edge and leave the task idle.
- For dependent chains, create downstream tasks as `planned`, create their automations, and let dependency completion or an explicit governed status transition move them to `ready`.
- Do not call manual automation run tools for normal flow. Use manual runs only for explicit smoke tests, diagnostics, or documented recovery.

Verification:

- Run the narrowest meaningful check first.
- Broaden verification when the change touches shared behavior or operational controls.
- Every production-code change must include explicit test coverage for the changed behavior before it can be considered complete. The default minimum is focused unit coverage plus the narrowest boundary, contract, handler, store, state-machine, or integration-style test that proves the behavior through the real production seam.
- Feature work must test success, failure, validation, authorization/privacy-relevant negative paths, persistence effects, observability-safe errors, and backward-compatibility behavior when those paths exist. If any class is not covered, the implementation handoff and review must name the invariant or code path that makes it impossible.
- Integration or contract tests are required when behavior crosses package, handler, store, database, queue, filesystem, MCP, REST, CLI, config, GitOps, automation, or generated-artifact boundaries. Use opt-in/local fakes or approved local services for runtime dependencies; do not downgrade boundary behavior to helper-only unit tests.
- Missing feasible tests are a blocker. Do not accept "manual tested", "low risk", "covered indirectly", "follow-up", or "time boxed" as substitutes for automated coverage when the path can be tested.
- Shallow tests are not acceptable for pipeline, automation, GitOps, verifier, review, closeout, branch, PR, or recovery changes. Coverage must prove the real contract across state, persistence, worker prompts, runner handoff, configured commands, retries, terminal failure, and downstream artifact shape.
- Every pipeline change must have a top-to-bottom test map before implementation: entry trigger, generated Work Plan/Work Task state, claim behavior, worker closeout, review gate, GitOps commit/push/PR ownership, verifier commands, generated artifacts, downstream CI/status checks when configured, recovery attempts, retry exhaustion, and final blocking state.
- A test plan is incomplete until it names edge cases that are covered or impossible by construction. Required edge cases include missing metadata, malformed refs, duplicate refs, stale claims, dirty worktrees, no-diff outputs, generated-artifact drift, verifier timeout, verifier failure, invalid branch/ticket refs, self-review, skipped review, concurrent runners, old terminal runs, partial GitOps success, and downstream retry after recovery.
- If the system can fail at a boundary, at least one regression test must exercise that boundary. Do not replace boundary coverage with prompt assertions, helper-only tests, or reviewer intuition.
- Test coverage for changed behavior must be contract-level and edge-case complete for the affected boundary. Do not accept a shallow unit, prompt-string, happy-path, or mocked-only test as sufficient when the real risk is in state transitions, persistence, runner/GitOps handoff, verifier execution, PR rendering, generated artifacts, concurrency, or recovery.
- Before implementation, write or update a test plan that names the success path, failure paths, negative cases, retry/recovery behavior, downstream handoff shape, and terminal blocking behavior for the exact boundary being changed. Implementation may proceed only after that coverage plan is reflected in tests or the untestable part is explicitly justified with the verifier that replaces it.
- For bug fixes and automation pipeline changes, add regression coverage for the exact failed contract before declaring the fix complete. Coverage must be broad enough to prove the behavior through the real state transition or integration boundary; shallow prompt/string/unit assertions are allowed only as supporting checks, never as the only proof.
- For workflow, automation, GitOps, verifier, branch, PR, or closeout changes, cover the full contract boundary in tests: invalid input blocked, valid input advances, dependencies preserved, retries/recovery bounded, terminal failure reported with a safe category/ref, and downstream stage receives the exact artifact shape it expects.
- When a downstream stage depends on an upstream artifact, test the handoff shape explicitly and in detail. Examples: Work Task dependency IDs, carried context refs, branch ticket refs, branch type, PR title/body values, verifier refs, review refs, generated-artifact refs, and recovery point refs.
- Edge cases are mandatory review input, not optional cleanup. For each changed pipeline contract, enumerate and cover or explicitly rule out: empty/missing metadata, malformed refs, duplicate refs, stale claims, already-completed tasks, failed dependencies, skipped review, self-review, dirty worktree, no diff, verifier timeout/failure, generated-artifact drift, retry exhaustion, concurrent runners, out-of-order completion, and downstream recovery after partial success.
- For automation/GitOps work, repository-specific lint, typecheck, test, and generated-artifact gates belong in config `[verification]` or `[projects.verification]`. Prompt instructions may remind agents, but the runner must enforce configured verification before commit, push, or draft PR.
- Generated artifacts that must be committed after a verifier runs must be declared in `generated_artifacts.paths`; do not rely on broad staging or ad hoc agent judgment.
- If a tool is unavailable, report the exact missing tool and residual risk.

Review:

- Reviews must verify behavior against source, tests, config, logs, and runtime metadata. Do not approve based only on prompt wording or claimed intent.
- Reviews of pipeline or automation changes must start with a coverage audit, not code style. If the tests do not prove the full affected boundary and required edge cases, the review blocks before implementation quality is considered.
- Reviewers must actively search for downstream blockers that the changed code can expose. For each downstream stage, verify whether the stage has a test for receiving the artifact shape produced by the previous stage and for rejecting malformed or stale artifacts safely.
- Reviews must start by checking whether the test plan covers the changed contract end to end. If it does not cover realistic negative, edge, retry, recovery, concurrency, and downstream handoff cases, the review blocks until tests are added or a named verifier proves the same behavior.
- For automation pipeline changes, reviewers must trace at least one successful path and one failure path through every affected stage boundary before approval.
- A review finding is actionable only when it names the violated contract, the reachable code path, the missing or weak test, and the failure impact.
- If coverage is insufficient for a high-risk path, the review must block or require additional tests. Do not convert missing tests into residual-risk prose when the path can be tested.
- Reviewers must reject changes that do not account for relevant edge cases. If an edge case is impossible by construction, the review must name the enforcing code or invariant.

Final handoff:

- Changed files.
- Verification performed.
- Risks remaining.
- Required human review or owner decisions.
