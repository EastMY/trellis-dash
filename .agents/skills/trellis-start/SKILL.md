---
name: trellis-start
description: "Start a Trellis session by classifying the request, checking current task state, and routing to the smallest appropriate workflow."
---

# Start Trellis Work

## 1. Establish authority

- Answer, explain, inspect, review, or diagnose: read and report only. Do not edit merely because a fix is obvious.
- Implement, change, build, or fix: scoped local edits and non-destructive validation are authorized.
- Ask before destructive actions, external writes, costly operations, or material scope expansion.
- Creating a Trellis task records work; it does not grant implementation authority. Conversely, an explicit implementation request remains authorized even when no Trellis task is created.

## 2. Inspect state

Read project `AGENTS.md` first, then run:

```bash
python3 ./.trellis/scripts/get_context.py
python3 ./.trellis/scripts/task.py current --source
```

If a task is active, load `trellis-continue`.

## 3. Triage task tracking

- Conversation, explanation, read-only review, or diagnosis: do not propose a task unless the user asks for one.
- Small bounded implementation that fits this session: skip task creation by default.
- Complex, risky, multi-package, independently deliverable, or likely multi-session work: recommend a Trellis task once and explain the concrete benefit.
- If the user declines task creation, continue the already-authorized work. Narrow scope only when necessary for safety or feasibility.

Never create a task without consent. When consent is given, run only `task.py create`; planning and activation are separate steps.

For a taskless small implementation, use the direct-work path: inspect enough context, edit only the authorized scope, run relevant non-destructive checks, keep unrelated dirty files untouched, and report the result. Do not create `verification.json`, run `completion-check`, archive, or journal unless a Trellis task was explicitly created.

## 4. Route tools

Follow project instructions. Prefer a configured structural index such as CodeGraph for definitions, callers, callees, and impact; use `rg` for literal text and file searches. If the index is unavailable, report the limitation and use the best read-only fallback.
