---
story_keys: []
validated_stories: []
---

# Step 1: Ingest Input

## RULES

- YOU MUST ALWAYS SPEAK OUTPUT in your Agent communication style with the config `{communication_language}`
- No human interaction until the confirmation checkpoint.
- **EARLY EXIT** means: stop this step immediately and read+follow the target step.

## INSTRUCTIONS

### 1. Parse story keys

Parse `{story_keys}` from the invocation prompt. Tokens are space-separated after the skill name.

Examples:
- `"loop 4-1 4-2 4-3"` → `["4-1", "4-2", "4-3"]`
- `"loop 4-1"` → `["4-1"]`
- `"loop epic-4"` → `["epic-4"]` (expanded below)

If `{story_keys}` is empty or none match the `N-N` or `epic-N` pattern, HALT with status `blocked` and blocking condition `no valid story keys found`.

### 2. Expand epic references

For each entry matching `epic-N`:
- Load `{sprint_status}` (the full file under `{implementation_artifacts}`).
- Find the epic with `id: N` in the `epics:` array.
- Collect all stories under that epic whose status is `ready-for-dev`.
- Replace the epic entry with the expanded story keys, preserving relative order.

### 3. Resolve story files

For each story key:

**A. Sprint-status lookup (preferred)**
- Load `{sprint_status}`.
- Find the story in the `epics[].stories[]` array by matching `key`.
  - If found and `status` is NOT `ready-for-dev`: log a warning (story key, current status) and **skip** this story.
  - If found and `status` IS `ready-for-dev` or not present in sprint status: proceed.
- Also check `development_status` section for a more granular status:
  - If status is `review`, `done`, or `blocked`: log a warning and **skip**.
  - If status is `ready-for-dev` or `in-progress` or missing: proceed.

**B. File resolution**
- Resolve path: `{implementation_artifacts}/{story_key}.md`
- If the file does not exist: log a warning (`story file not found: {story_key}`) and **skip**.

**C. Collect validated story**
Add to `{validated_stories}` as:
```yaml
- key: "4-1"
  path: "{implementation_artifacts}/4-1-dry-run-mode.md"
  title: "dry-run-mode"
  status: "ready-for-dev"
```

### 4. Display plan

After processing all keys:

```
🔄 Loop Plan ({N} stories):

  [1/3] 4-1 — dry-run-mode        (ready-for-dev)
  [2/3] 4-2 — posix-compliant-cli (ready-for-dev)
  [3/3] 4-3 — ide-extension-socket (ready-for-dev)

⚠️ Skipped (not ready):
  - 5-1 — token-savings-calculator (review)
```

If N = 0: HALT with status `blocked`, blocking condition `no ready-for-dev stories in input list`.

### 5. Confirm with user

Ask: "Ready to start the loop? This will process {N} stories sequentially."

Wait for user confirmation before proceeding.

If user declines or modifies the list, re-validate and display updated plan.

### 6. Initialize loop state

Create `{loop_status_file}`:
```yaml
# Loop Status
started: "{date}"
total_stories: {N}
stories:
  - key: "4-1"
    status: pending
  - key: "4-2"
    status: pending
  - key: "4-3"
    status: pending
```

## NEXT

Read fully and follow `./step-02-execute-loop.md`
