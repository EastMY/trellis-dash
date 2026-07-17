---
name: trellis-check
description: "Run risk-proportionate verification for the current Trellis task, classify failures, make only in-scope fixes, and record structured evidence."
---

# Trellis Quality Check

## 1. Scope the review

Read project `AGENTS.md`, the current diff, and only applicable specs. If a Trellis task is active, also read `prd.md` plus optional design/implementation artifacts and map each acceptance criterion to evidence. For taskless direct work, verify the explicit user request and changed behavior instead.

Follow project tool routing. Use a configured structural index for definitions/callers/impact and `rg` for literal text; fall back transparently if the index is unavailable.

## 2. Choose relevant checks

Run validation proportional to changed behavior and repository guidance: focused tests first, then broader lint/type-check/build when risk or project completion criteria require them. Do not run every available suite by default.

For each result classify failures as:

- `current-task`: caused by or blocking this task;
- `pre-existing`: reproduced outside the task change;
- `environment`: missing service, credentials, device, network, or platform;
- `tooling`: broken/unavailable verifier;
- `uncertain`: evidence is insufficient.

Fix only current-task issues that are within authorized scope. Small mechanical fixes are allowed; design or scope changes return to the main session/user.

Retry the same unchanged check at most twice after targeted fixes. If it still fails, stop, preserve the output, classify it, and report the next discriminating action. When a check cannot run, explain why and use the best available substitute.

## 3. Review dimensions

- Acceptance criteria and user-visible behavior.
- Relevant tests and regression coverage.
- Type, lint, build, or runtime checks required by the project.
- Cross-layer contracts when the change spans layers.
- Security, data integrity, compatibility, and rollback risks when applicable.
- No unrelated changes or suppressed failures.

## 4. Record evidence

For an active Trellis task, create or update `<task>/verification.json`:

```json
{
  "schemaVersion": 1,
  "status": "passed",
  "acceptance": "passed",
  "summary": "What was verified",
  "checks": [
    {"command": "exact command", "result": "passed", "reason": "optional note"}
  ],
  "workCommits": []
}
```

Allowed check results are `passed`, `failed`, and `skipped`; skipped entries require a reason. Set overall status/acceptance to `passed` only when the evidence supports it. Phase 3.4 fills `workCommits` after committing.

For taskless direct work, skip `verification.json` and report the same evidence directly.

Report findings, fixes, open risks, and exact validation results. Never commit or push from this skill.
