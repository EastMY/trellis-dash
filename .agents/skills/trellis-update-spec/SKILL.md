---
name: trellis-update-spec
description: "Decide whether a task produced durable project knowledge and, only when justified, update the smallest relevant .trellis/spec document."
---

# Update Trellis Spec

Phase 3.3 always makes a knowledge-capture decision; it does not always edit a file.

## Update when knowledge is durable

Write a spec update when the task established or corrected at least one of:

- a stable API, command, schema, environment, or cross-layer contract;
- a project-specific convention or reusable implementation pattern;
- a repeated or non-obvious gotcha with a concrete prevention rule;
- an architectural decision future changes must preserve;
- an existing spec that the implementation proved outdated or wrong.

Skip updates for one-off details, generic engineering advice, information already documented, temporary diagnostics, or facts cheaply recoverable from code without ambiguity. Record the skip decision briefly in the task/session evidence; do not create filler documentation.

## Update process

1. State the durable fact and the failure it prevents.
2. Read the relevant spec index and target document; avoid duplication.
3. Edit the smallest correct location. Use a guide only for a short thinking checklist; put executable implementation contracts in the package/layer spec.
4. Include only sections the contract needs: signature/fields, invariants, error behavior, example, migration/compatibility, or validation points. No fixed seven-section template is required.
5. Validate links, commands, examples, and consistency with the implemented code.

For a narrow gotcha, a short warning plus prevention/test may be enough. For a new cross-layer contract, include the complete request/response or data-flow details required to implement it safely.

Do not sync nonexistent template paths. Do not create tickets, commit, or push from this skill; Phase 3.4 owns commits.
