---
name: trellis-continue
description: "Resume the active Trellis task from its persisted state without repeating completed gates."
---

# Continue Trellis Work

Run:

```bash
python3 ./.trellis/scripts/task.py current --source
python3 ./.trellis/scripts/get_context.py
```

Read the active task's `task.json`, `prd.md`, optional `design.md`, optional `implement.md`, and `verification.json` when present. Resume from evidence, not conversation memory.

## Routing

- `status=planning`: load `trellis-brainstorm`. Fill only missing material decisions and artifacts. Activate when the plan is sufficient and the original request already authorizes implementation; ask again only if the user requested planning-only or the scope materially expanded.
- `status=in_progress`, implementation incomplete: follow Phase 2.1.
- `status=in_progress`, implementation complete but checks missing/failed: load `trellis-check` and update `verification.json`.
- `status=in_progress`, checks passed: make the Phase 3.3 knowledge-capture decision, then Phase 3.4 commits. Add the resulting work commit hashes to `verification.json`, run `completion-check`, and only then run `trellis-finish-work`.
- `status=completed`: normally already archived; report stale state instead of inventing work.

Do not redo a completed gate. Do not treat task tracking as new implementation authorization.
