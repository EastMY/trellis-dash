---
name: trellis-break-loop
description: "Diagnose repeated failed fixes or perform an explicitly requested debugging retrospective without silently changing code, specs, templates, or Git state."
---

# Break a Debugging Loop

Use this skill after the same symptom or check has failed across at least two materially different fix attempts, or when the user explicitly requests a retrospective.

## Default boundary

This skill is read-only. It may inspect code, logs, diffs, tests, task artifacts, and history, then report a diagnosis. It must not edit code or specs, sync templates, create tickets, commit, or push unless the user separately authorizes that action through the normal workflow.

## Analysis

1. State the exact symptom and the evidence that reproduces it.
2. Build a short timeline: attempted change → observed result → what that result rules in or out.
3. Separate confirmed facts, plausible hypotheses, and unknowns. Do not invent probability percentages.
4. Trace the relevant boundary end to end: input, transformation, persistence, output, and environment where applicable.
5. Identify the earliest point where actual behavior diverges from the expected contract.
6. Propose the smallest discriminating experiment or fix, its risk, and its validation.

Stop repeating the same command after two unchanged failures. Classify the blocker as current-task defect, pre-existing defect, environment, tooling, or uncertain, then report the next useful action.

## Output

- Root cause: confirmed, likely, or still unknown.
- Why prior attempts failed.
- Evidence and reproduction.
- Recommended next action and validation.
- Durable knowledge candidate, if any.

A spec update is only a recommendation here. Apply it in Phase 3.3 with `trellis-update-spec` when authorized and worthwhile; commits remain Phase 3.4.
