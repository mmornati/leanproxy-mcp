#!/usr/bin/env python3
import os
import sys
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
    is_direct_push_to_main,
    is_story_implemented,
    load_mapping,
    parse_story_title,
    update_comment,
    update_pr_body,
)


def process_delivery(repo: str, token: str, mapping: dict, file_path: str, content: str, pr_number: int | None = None) -> dict:
    file_stem = Path(file_path).stem

    if not is_story_implemented(content):
        print(f"Skipping {file_stem}: story not yet implemented")
        return mapping

    story_title = parse_story_title(content) or file_stem
    issue_number = get_issue_from_mapping(mapping, file_stem)

    if not issue_number:
        print(f"WARNING: No issue found in mapping for {file_stem}. Cannot deliver story.")
        return mapping

    commit_sha = get_file_commit_sha(file_path)
    commit_author = get_commit_author(file_path)
    repo_env = os.environ.get("GITHUB_REPOSITORY", "")

    is_direct = is_direct_push_to_main()

    if is_direct:
        print(f"Direct push to main for {story_title} - assigning and closing issue #{issue_number}")
        if commit_author:
            assign_issue(repo, token, issue_number, commit_author)
            print(f"  Assigned to {commit_author}")
        close_issue(repo, token, issue_number)
        print(f"  Closed issue #{issue_number}")
    elif pr_number:
        print(f"PR delivery for {story_title} - PR #{pr_number} to issue #{issue_number}")

        closes_marker = f"Closes #{issue_number}"
        update_pr_body(repo, token, pr_number, closes_marker)
        print(f"  Added '{closes_marker}' to PR #{pr_number} body")

        if commit_author:
            assign_issue(repo, token, issue_number, commit_author)
            print(f"  Assigned issue #{issue_number} to {commit_author}")

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

    if not repo or not token:
        print("ERROR: GITHUB_REPOSITORY and GITHUB_TOKEN must be set")
        sys.exit(1)

    if pr_number:
        try:
            pr_number = int(pr_number)
        except ValueError:
            pr_number = None

    mapping = load_mapping()

    from bmad_sync_lib import get_changed_files
    all_new, all_mod = get_changed_files()
    all_files = list(set(all_new + all_mod))
    story_files = [f for f in all_files if "implementation-artifacts/" in f and f.endswith(".md")]

    for file_path in story_files:
        content = Path(file_path).read_text()
        mapping = process_delivery(repo, token, mapping, file_path, content, pr_number)

    from bmad_sync_lib import save_mapping
    save_mapping(mapping)

    print(f"Story delivery completed. Processed {len(story_files)} story files.")


if __name__ == "__main__":
    main()