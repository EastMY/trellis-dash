---
name: trellis-brainstorm
description: "Turn an unclear or complex request into sufficient Trellis planning artifacts using repository evidence and only material user questions."
---

# Trellis Brainstorm

The goal is sufficient shared understanding, not maximum questioning.

## 1. Investigate before asking

Read project `AGENTS.md`, existing task artifacts, relevant specs, and repository evidence first. Follow project tool routing: use a configured structural index for symbol relationships and `rg` for literal text. Do not ask the user for facts the repository or authoritative documentation can answer.

## 2. Ask only material questions

Ask when the answer changes scope, behavior, compatibility, data handling, rollout, or acceptance criteria.

- Ask zero questions when the request and evidence are already sufficient.
- Ask one at a time when answers depend on each other.
- Up to three independent questions may be grouped when that reduces needless turns.
- Prefer concise options with consequences over a vague open question.
- Stop when goal, boundaries, constraints, acceptance criteria, and important edge cases are clear enough to implement safely.

Do not explore every hypothetical branch. Record assumptions that are low-risk and reversible; ask before assumptions that materially change the result.

## 3. Persist durable planning

Update `prd.md` at material milestones: after a decision changes the contract, after research changes the approach, or when the plan converges. Do not rewrite it after every conversational answer.

`prd.md` contains:

- goal and user-visible outcome;
- in-scope and out-of-scope boundaries;
- requirements and constraints;
- testable acceptance criteria;
- unresolved blocking decisions, if any.

For complex work, also create:

- `design.md`: architecture, interfaces, data flow, compatibility, tradeoffs, rollback;
- `implement.md`: ordered change plan, validation commands, review gates.

Use parent/child tasks only for independently verifiable deliverables. Write dependencies explicitly; tree position does not imply order.

## 4. Research selectively

Persist research when it affects implementation, must retain source evidence, or will be reused across sessions. One summary file is the default; split files only for genuinely independent topics. Ephemeral lookups may remain in the working notes or response.

## 5. Activation gate

Before `task.py start`, verify required artifacts are sufficient and no blocking decision remains. If the user's original request already says implement/change/fix/build, activation does not require a redundant confirmation. Ask before activation only for planning-only requests, material scope expansion, or a choice the user must make.
