#!/usr/bin/env python3
import os
import sys
import json
from pathlib import Path

from bmad_sync_lib import (
    add_comment,
    assign_issue,
    close_issue,
    find_delivery_comment,
    format_timestamp,
    get_comments,
    get_commit_author,
    get_file_commit_sha,
    get_issue_from_mapping,
    get_pr_author,
    get_issue_state_and_assignees,
    is_direct_push_to_main,
    is_story_implemented,
    load_mapping,
    parse_story_title,
    update_comment,
    update_pr_body,
    get_changed_files,
)


DEBUG = os.environ.get("DEBUG_BMAD_DELIVERY", "false").lower() == "true"

BOT_NAMES = ("bmad bot", "github-actions[bot]", "github actions", "bot")


def is_bot_name(author: str | None) -> bool:
    if not author:
        return True
    return author.lower() in BOT_NAMES


def get_assignee_for_delivery(repo: str, token: str, file_path: str, pr_number: int | None, is_direct: bool) -> str | None:
    github_actor = os.environ.get("GITHUB_ACTOR")

    if pr_number and not is_direct:
        pr_author = get_pr_author(repo, token, pr_number)
        if pr_author:
            return pr_author

    commit_author = get_commit_author(file_path)

    if is_bot_name(commit_author):
        return github_actor

    return commit_author


def process_delivery(repo: str, token: str, mapping: dict, file_path: str, content: str, pr_number: int | None = None) -> dict:
    file_stem = Path(file_path).stem

    if DEBUG:
        print(f"[DEBUG] Processing delivery for: {file_stem}")

    story_implemented = is_story_implemented(content)
    if DEBUG:
        print(f"[DEBUG] is_story_implemented result: {story_implemented}")

    if not story_implemented:
        print(f"Skipping {file_stem}: story not yet implemented")
        return mapping

    story_title = parse_story_title(content) or file_stem
    issue_number = get_issue_from_mapping(mapping, file_stem)

    if DEBUG:
        print(f"[DEBUG] story_title: {story_title}, issue_number: {issue_number}")

    if not issue_number:
        print(f"WARNING: No issue found in mapping for {file_stem}. Cannot deliver story.")
        return mapping

    commit_sha = get_file_commit_sha(file_path)
    repo_env = os.environ.get("GITHUB_REPOSITORY", "")

    is_direct = is_direct_push_to_main()
    if DEBUG:
        print(f"[DEBUG] is_direct_push_to_main: {is_direct}, GITHUB_REF: {os.environ.get('GITHUB_REF_NAME', 'unknown')}")

    issue_state, issue_assignees = get_issue_state_and_assignees(repo, token, issue_number)
    if DEBUG:
        print(f"[DEBUG] issue_state: {issue_state}, issue_assignees: {issue_assignees}")

    if is_direct:
        print(f"Direct push to main for {story_title} - assigning and closing issue #{issue_number}")
        assignee = get_assignee_for_delivery(repo, token, file_path, pr_number, is_direct)
        if assignee and issue_state != "closed":
            if issue_assignees:
                print(f"  Skipping assignment - issue #{issue_number} already assigned to: {', '.join(issue_assignees)}")
            else:
                if assign_issue(repo, token, issue_number, assignee):
                    print(f"  Assigned to {assignee}")
        close_issue(repo, token, issue_number)
        print(f"  Closed issue #{issue_number}")
    elif pr_number:
        print(f"PR delivery for {story_title} - PR #{pr_number} to issue #{issue_number}")

        closes_marker = f"Closes #{issue_number}"
        update_pr_body(repo, token, pr_number, closes_marker)
        print(f"  Added '{closes_marker}' to PR #{pr_number} body")

        assignee = get_assignee_for_delivery(repo, token, file_path, pr_number, is_direct)
        if assignee:
            if issue_assignees:
                print(f"  Skipping assignment - issue #{issue_number} already assigned to: {', '.join(issue_assignees)}")
            elif issue_state == "closed":
                print(f"  Skipping assignment - issue #{issue_number} is already closed")
            else:
                if assign_issue(repo, token, issue_number, assignee):
                    print(f"  Assigned issue #{issue_number} to {assignee}")

        timestamp = format_timestamp()
        comment = f"""**Story Delivered** - {timestamp}

Story [{story_title}](https://github.com/{repo_env}/blob/main/{file_path}) has been delivered via PR #{pr_number}.

Commit: [{commit_sha}](https://github.com/{repo_env}/commit/{commit_sha})
"""

        existing_comments = get_comments(repo, token, issue_number)
        existing_delivery = find_delivery_comment(existing_comments)
        if existing_delivery:
            update_comment(repo, token, existing_delivery["id"], comment)
            print(f"  Updated delivery comment on issue #{issue_number}")
        else:
            add_comment(repo, token, issue_number, comment)
            print(f"  Added delivery comment to issue #{issue_number}")
    else:
        print(f"  WARNING: No PR number available and not direct push to main. Cannot deliver via PR.")

    return mapping


def main():
    repo = os.environ.get("GITHUB_REPOSITORY")
    token = os.environ.get("GITHUB_TOKEN")
    pr_number = os.environ.get("PULL_REQUEST_NUMBER")

    if DEBUG:
        print(f"[DEBUG] GITHUB_REPOSITORY: {repo}")
        print(f"[DEBUG] PULL_REQUEST_NUMBER: {pr_number}")
        print(f"[DEBUG] GITHUB_REF_NAME: {os.environ.get('GITHUB_REF_NAME', 'unknown')}")
        print(f"[DEBUG] GITHUB_HEAD_REF: {os.environ.get('GITHUB_HEAD_REF', 'unknown')}")
        print(f"[DEBUG] GITHUB_ACTOR: {os.environ.get('GITHUB_ACTOR', 'unknown')}")

    if not repo or not token:
        print("ERROR: GITHUB_REPOSITORY and GITHUB_TOKEN must be set")
        sys.exit(1)

    if pr_number:
        try:
            pr_number = int(pr_number)
        except ValueError:
            pr_number = None

    mapping = load_mapping()

    all_new, all_mod = get_changed_files()
    all_files = list(set(all_new + all_mod))
    story_files = [f for f in all_files if "implementation-artifacts/" in f and f.endswith(".md")]

    if DEBUG:
        print(f"[DEBUG] Changed files: {all_files}")
        print(f"[DEBUG] Story files found: {story_files}")

    for file_path in story_files:
        content = Path(file_path).read_text()
        mapping = process_delivery(repo, token, mapping, file_path, content, pr_number)

    from bmad_sync_lib import save_mapping
    save_mapping(mapping)

    print(f"Story delivery completed. Processed {len(story_files)} story files.")


if __name__ == "__main__":
    main()