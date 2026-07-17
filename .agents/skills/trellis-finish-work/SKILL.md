---
name: trellis-finish-work
description: "Archive a completed Trellis task only after semantic and automated completion gates pass, then record the session journal."
---

# Finish Trellis Work

Work commits happen in Phase 3.4. This skill performs the final gate, archive, and journal; it does not commit product code or push.

## 1. Identify the exact task and commits

```bash
python3 ./.trellis/scripts/get_context.py --mode record
python3 ./.trellis/scripts/task.py current --source
```

Confirm that the active task, PRD, changed behavior, and Phase 3.4 work commits describe the same deliverable. Put only those work commit hashes in `<task>/verification.json.workCommits`.

## 2. Run the completion gate

```bash
python3 ./.trellis/scripts/task.py completion-check <task-dir>
```

Do not archive while any `BLOCKER` remains. Review every `WARNING` semantically rather than treating exit code 0 as sufficient:

- confirm work commits belong to this task;
- confirm PRD acceptance items match actual behavior;
- classify every dirty path outside the current task directory as current-task or unrelated parallel work, including paths under other `.trellis/tasks/` directories;
- confirm skipped checks have a justified limitation and best available substitute.

If a dirty path belongs to the task, return to Phase 3.4. Leave unrelated user/parallel changes untouched and report them once. Never include another task directory in the current archive/journal bookkeeping. If ownership is genuinely unclear, ask one concise question.

## 3. Archive only after the gate passes

```bash
python3 ./.trellis/scripts/task.py archive <task-dir>
```

Archive the current task only. Other apparently completed tasks require separate user confirmation. The archive command may create its bookkeeping commit according to project configuration.

## 4. Record the session

```bash
python3 ./.trellis/scripts/add_session.py \
  --title "Session Title" \
  --commit "work-hash1,work-hash2" \
  --summary "Brief outcome and validation"
```

Use Phase 3.4 work commits, not archive/journal commit hashes. Final order remains: work commits → archive commit → journal commit.
