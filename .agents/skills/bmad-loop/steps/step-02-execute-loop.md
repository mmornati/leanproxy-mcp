---
current_index: 2
---

# Step 2: Execute Loop

## RULES

- YOU MUST ALWAYS SPEAK OUTPUT in your Agent communication style with the config `{communication_language}`
- No human interaction during the loop body. Only the confirmation at step-01 and a final summary at the end are interactive.
- Subagents are synchronous — launch, wait for return, continue. No background/detached execution.
- Do NOT skip stories in `{validated_stories}`. Process in order.
- If `{loop_status_file}` exists and has stories with status `done` or `in-progress`, check for resume: find the first story that is NOT `done`. If `current_index` is present — resume from there. If all are `done`, proceed to final summary.

## LOOP INIT

Load `{loop_status_file}` to determine resume point. Resume logic:
- If `current_index` exists in frontmatter: resume from that index.
- Otherwise start at index 0.
- Update `current_index` in frontmatter as each story completes.

Set `branch_prefix = "story/"` (configurable via customize `branch_prefix` if overridden).

## PER-STORY LOOP BODY

For each story at `{current_index}` in `{validated_stories}`:

### Phase 1 — Dev Subagent

Update `{loop_status_file}`: set this story's `status` to `dev-in-progress`.

Launch a subagent synchronously with this prompt:

> **Prompt to dev subagent:**
>
> You are executing the **bmad-dev-story** workflow. First, read and follow the instructions in the SKILL.md located at:
> `{skill-root}/../bmad-dev-story/SKILL.md`
>
> Your task is to implement the story file at:
> `{story.path}`
>
> Important rules:
> - Run the complete workflow autonomously. Do not ask the user for input.
> - Implement ALL tasks/subtasks in the story.
> - Run ALL tests and fix any failures.
> - Set the story status to "review" when complete.
>
> When done, return:
> 1. A summary of what was implemented (2-3 sentences)
> 2. List of files created or modified
> 3. The git commit hash of the last commit (run `git rev-parse HEAD`)
> 4. Any warnings or issues encountered

Wait for the subagent to return. Store its output as `{dev_result}`.

If the subagent returns an error or the story was not completed, HALT with status `blocked` and blocking condition `story dev failed: {story.key} — {error_details}`.

Update `{loop_status_file}`: set `status` to `dev-done`.

### Phase 2 — Review Subagent

Update `{loop_status_file}`: set `status` to `review-in-progress`.

Build the review prompt. If `{review_model_override}` is non-empty, prepend a model routing instruction:

> **Prompt to review subagent (with model override):**
>
> Note: If your runtime supports model routing, use the model: `{review_model_override}` for this session.
>
> You are executing the **bmad-code-review** workflow. First, read and follow the instructions in the SKILL.md located at:
> `{skill-root}/../bmad-code-review/SKILL.md`
>
> Your task is to review the changes made for story `{story.key}`.
>
> Important rules:
> - Run the complete workflow autonomously. Do not ask the user for input.
> - Review the current state of the repository (the changes from the dev phase).
> - Produce the standard triage output (intent_gap, bad_spec, patch, defer, reject).
>
> When done, return:
> 1. A summary of review findings (2-3 sentences)
> 2. Severity breakdown (high/medium/low counts)
> 3. Any blocking issues or change requests
> 4. Whether the review recommends changes before merging

> **Prompt to review subagent (without model override, i.e. empty):**
>
> You are executing the **bmad-code-review** workflow. First, read and follow the instructions in the SKILL.md located at:
> `{skill-root}/../bmad-code-review/SKILL.md`
>
> Your task is to review the changes made for story `{story.key}`.
>
> Important rules:
> - Run the complete workflow autonomously. Do not ask the user for input.
> - Review the current state of the repository (the changes from the dev phase).
> - Produce the standard triage output (intent_gap, bad_spec, patch, defer, reject).
>
> When done, return:
> 1. A summary of review findings (2-3 sentences)
> 2. Severity breakdown (high/medium/low counts)
> 3. Any blocking issues or change requests
> 4. Whether the review recommends changes before merging

Wait for the subagent to return. Store its output as `{review_result}`.

If the review found high-severity issues or requested changes, fix them before proceeding:
- Launch a fix subagent with the review findings and instruct it to address them.
- Wait for it to return.
- Proceed.

Update `{loop_status_file}`: set `status` to `review-done`.

### Phase 3 — Branch, Commit, PR

Update `{loop_status_file}`: set `status` to `pr-in-progress`.

**A. Determine branch name**

```
{branch_prefix}{story.key}
```

Example: `story/4-1-dry-run-mode`

**B. Create branch**

Run:
```bash
git checkout -b {branch_name}
```

If the branch already exists locally, delete it first:
```bash
git branch -D {branch_name} 2>/dev/null; git checkout -b {branch_name}
```

**C. Commit changes**

Gather commit message from `{dev_result}`. Use the summary as the commit body.

```bash
git add -A
git commit -m "feat({story.key}): {short description from dev_result}"
```

If there are review-fix changes, include a second commit:
```bash
git add -A
git commit -m "fix({story.key}): address code review findings"
```

**D. Push**

```bash
git push origin {branch_name}
```

If push fails due to upstream existing, force-push:
```bash
git push --force origin {branch_name}
```

**E. Create PR**

Build a detailed PR body from `{dev_result}` and `{review_result}`:

```
## Summary
{dev_result.summary}

## Files Changed
{dev_result.files_changed}

## Review Findings
{review_result.summary}
- High: {review_result.high_count}
- Medium: {review_result.medium_count}
- Low: {review_result.low_count}
```

Run:
```bash
gh pr create \
  --title "Story {story.key}: {story.title}" \
  --body "{pr_body}" \
  --base main
```

Capture the PR number from output (e.g. `https://github.com/.../pull/123` → `123`). Store as `{pr_number}`.

Update `{loop_status_file}`: set `status` to `pr-created`, set `pr_number`.

### Phase 4 — CI Check Loop

Update `{loop_status_file}`: set `status` to `ci-in-progress`.

Set `retry_count = 0` and `max_retries = {ci_max_retries}`.

**Loop:**

A. Wait `{ci_poll_interval_seconds}` seconds (use `sleep`).

B. Check CI status:
```bash
gh pr checks {pr_number} 2>&1
```

C. Parse the output. Look for the conclusion line (last line):
- If output contains "pass", "success", or "All checks were successful": CI passed. Break.
- If output contains "fail", "failing", or "failure": CI failed.
- Otherwise (pending, cancelled, skipped): continue polling.

D. **If CI passed** → proceed to Phase 5.

E. **If CI failed AND `retry_count < max_retries`**:
- Increment `retry_count`.
- Launch a fix subagent with this prompt:

> **Prompt to CI fix subagent:**
>
> The CI pipeline failed for PR #{pr_number} on branch `{branch_name}`.
>
> CI output:
> ```
> {ci_output}
> ```
>
> Diagnose the failure and fix it. Make minimal, targeted changes. Do not introduce unrelated changes.
>
> When done, return:
> 1. What caused the failure
> 2. What was changed to fix it
> 3. Any residual risks

- Wait for the fix subagent to return.
- Commit and push fixes:
  ```bash
  git add -A
  git commit -m "fix({story.key}): address CI failure (attempt {retry_count}/{max_retries})"
  git push origin {branch_name}
  ```
- Loop back to A (re-poll CI).

F. **If CI failed AND `retry_count >= max_retries`**:
- HALT with status `blocked` and blocking condition `CI failures persisted after {max_retries} retries for story {story.key} (PR #{pr_number})`.

If CI is pending for too long (total polling time exceeds 30 minutes), HALT with status `blocked` and blocking condition `CI timeout for story {story.key} (PR #{pr_number})`.

Update `{loop_status_file}`: set `status` to `ci-passed`.

### Phase 5 — Merge

Update `{loop_status_file}`: set `status` to `merge-in-progress`.

Run:
```bash
gh pr merge {pr_number} --{merge_strategy} --delete-branch
```

If merge fails (conflicts, protection rules), HALT with status `blocked` and blocking condition `merge failed for story {story.key} (PR #{pr_number})`.

Update `{loop_status_file}`: set `status` to `merged`.

### Phase 6 — Prepare for Next Story

```bash
git checkout main
git pull origin main
```

Update `{current_index}` in frontmatter: increment by 1.

## AFTER ALL STORIES

### Final Summary

Print a complete summary:

```
✅ Loop Complete — {N} stories processed:

  {story.key} — ✅ PR #{pr_number}: merged, all CI passed
  {story.key} — ✅ PR #{pr_number}: merged, all CI passed
  ...

Total time: ~{duration}
```

### Update Loop Status

Set `{loop_status_file}` overall status to `done`.

### HALT

HALT with status `done`.
