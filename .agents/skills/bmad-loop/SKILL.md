---
name: bmad-loop
description: 'Iterate over a list of story keys, executing dev → review → PR → CI → merge for each. Use when the user says "loop these stories [list]" or "run the loop on [story keys]"'
---

# Loop Workflow

**Goal:** Deliver each story in an input list through the full pipeline: implement, review, branch, PR, CI-check, merge — then move to the next.

**Your Role:** Automated delivery orchestrator. You coordinate subagents, manage git, and drive PRs to merge. No human interaction inside the loop body.

## HALT

To HALT with a final status and optional blocking condition:

1. If applicable, append a loop-status entry to `{loop_status_file}`.
2. Run: `python3 {project-root}/_bmad/scripts/resolve_customization.py --skill {skill-root} --key workflow.on_complete`
3. If the resolved `workflow.on_complete` is non-empty, follow it as the final instruction before exiting.
4. Stop the workflow.

## Subagents

Using subagents when instructed is mandatory. If you cannot, HALT with status `blocked` and blocking condition `no subagents`.

Invoke every subagent **synchronously**: launch it, wait for it to return within the same turn, then continue with its result. Never run a subagent in the background / detached / async (e.g. `run_in_background: true`), and never end your turn to await a completion notification. This workflow runs unattended: there is no event loop to resume a yielded turn, so a backgrounded subagent never hands control back and the run stalls. The only sanctioned way to end a turn is the HALT protocol above with an explicit terminal `status`.

## Conventions

- Bare paths (e.g. `steps/step-01-ingest-input.md`) resolve from the skill root.
- `{skill-root}` resolves to this skill's installed directory (where `customize.toml` lives).
- `{project-root}`-prefixed paths resolve from the project working directory.
- `{skill-name}` resolves to the skill directory's basename.

## Input Resolution

Parse the invocation prompt for space-separated story keys (e.g. `"4-1 4-2 4-3"`). Store as `{story_keys}` array, preserving order. If the prompt contains no recognizable story keys, HALT with status `blocked` and blocking condition `no story keys provided`.

Supported patterns per element:
- `N-N` — story key like `4-1`
- `epic-N` — epic key like `epic-4` (expanded later by step-01)

## On Activation

### Step 1: Resolve the Workflow Block

Run: `python3 {project-root}/_bmad/scripts/resolve_customization.py --skill {skill-root} --key workflow`

**If the script fails**, resolve the `workflow` block yourself by reading these three files in base → team → user order and applying the same structural merge rules as the resolver:

1. `{skill-root}/customize.toml` — defaults
2. `{project-root}/_bmad/custom/{skill-name}.toml` — team overrides
3. `{project-root}/_bmad/custom/{skill-name}.user.toml` — personal overrides

Any missing file is skipped. Scalars override, tables deep-merge, arrays of tables keyed by `code` or `id` replace matching entries and append new entries, and all other arrays append.

### Step 2: Execute Prepend Steps

Execute each entry in `{workflow.activation_steps_prepend}` in order before proceeding.

### Step 3: Load Persistent Facts

Treat every entry in `{workflow.persistent_facts}` as foundational context you carry for the rest of the workflow run. Entries prefixed `file:` are paths or globs under `{project-root}` — load the referenced contents as facts. All other entries are facts verbatim.

### Step 4: Load Config

Load config from `{project-root}/_bmad/bmm/config.yaml` and resolve:

- `project_name`, `planning_artifacts`, `implementation_artifacts`, `user_name`
- `communication_language`, `document_output_language`, `user_skill_level`
- `date` as system-generated current datetime
- `sprint_status` = `{implementation_artifacts}/sprint-status.yaml`
- `project_context` = `**/project-context.md` (load if exists)
- YOU MUST ALWAYS SPEAK OUTPUT in your Agent communication style with the config `{communication_language}`
- Language MUST be tailored to `{user_skill_level}`
- Generate all documents in `{document_output_language}`

Resolve customization overrides:

- `review_model_override` = resolved value from `{workflow.review_model_override}` (default: empty)
- `ci_poll_interval_seconds` = resolved value from `{workflow.ci_poll_interval_seconds}` (default: 30)
- `ci_max_retries` = resolved value from `{workflow.ci_max_retries}` (default: 3)
- `merge_strategy` = resolved value from `{workflow.merge_strategy}` (default: squash)
- `ci_fix_agent_model_override` = resolved value from `{workflow.ci_fix_agent_model_override}` (default: empty)

### Step 5: Greet the User

Greet `{user_name}`, speaking in `{communication_language}`.

### Step 6: Execute Append Steps

Execute each entry in `{workflow.activation_steps_append}` in order.

Activation is complete. If `activation_steps_prepend` or `activation_steps_append` were non-empty, confirm every entry was executed in order before proceeding. Do not begin the main workflow until all activation steps have been completed.

## Paths

- `sprint_status` = `{implementation_artifacts}/sprint-status.yaml`
- `loop_status_file` = `{implementation_artifacts}/loop-status.yaml`

## First workflow step

Read fully and follow: `./steps/step-01-ingest-input.md` to begin the workflow.
