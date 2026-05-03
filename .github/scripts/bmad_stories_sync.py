#!/usr/bin/env python3
import os
import sys
from pathlib import Path

from bmad_sync_lib import (
    add_comment,
    build_issue_body,
    commit_file_to_git,
    create_issue,
    ensure_label_exists,
    extract_epic_key,
    find_issue_by_title,
    find_source_changed_comment,
    format_timestamp,
    get_comments,
    get_file_commit_sha,
    get_file_commit_url,
    link_sub_issue,
    load_mapping,
    parse_story_title,
    save_mapping,
    update_comment,
    update_issue,
)

LABEL_NAME = "bmad"

BOT_NAMES = ("bmad bot", "github-actions[bot]", "github actions", "bmad-bot", "bot")


def is_bot_author(file_path: str) -> bool:
    from bmad_sync_lib import get_commit_author
    author = get_commit_author(file_path)
    if not author:
        return True
    return author.lower() in BOT_NAMES


def process_story(repo: str, token: str, mapping: dict, file_path: str, content: str) -> dict:
    file_stem = Path(file_path).stem

    if is_bot_author(file_path):
        print(f"Skipping {file_stem}: commit author is a bot, skipping sync")
        return mapping

    story_title = parse_story_title(content) or file_stem
    epic_key = extract_epic_key(content, file_stem)

    existing_issue = None
    if file_stem in mapping.get("stories", {}):
        mapped = mapping["stories"][file_stem]
        existing_issue = {"number": mapped["issue_number"], "id": mapped["issue_id"]}
    else:
        existing_issue = find_issue_by_title(repo, token, story_title)

    if existing_issue:
        issue_number = existing_issue["number"]
        repo_env = os.environ.get("GITHUB_REPOSITORY", "")
        commit_sha = get_file_commit_sha(file_path)
        timestamp = format_timestamp()

        comment = f"""**BMad Source Changed** - {timestamp}

Story [{story_title}](https://github.com/{repo_env}/blob/main/{file_path}) was updated in BMAD source.

Commit: [{commit_sha}](https://github.com/{repo_env}/commit/{commit_sha})
"""

        existing_comments = get_comments(repo, token, issue_number)
        existing_source_changed = find_source_changed_comment(existing_comments)

        if existing_source_changed:
            update_comment(repo, token, existing_source_changed["id"], comment)
            print(f"Updated story issue #{issue_number}: {story_title} (updated modification comment)")
        else:
            add_comment(repo, token, issue_number, comment)
            print(f"Updated story issue #{issue_number}: {story_title} (added modification comment)")

        mapping["stories"][file_stem] = {
            "issue_id": existing_issue["id"],
            "issue_number": issue_number,
            "parent_epic": epic_key,
            "commit_sha": commit_sha,
        }
    else:
        ensure_label_exists(repo, token)

        parent_epic_number = None
        if epic_key and epic_key in mapping["epics"]:
            parent_epic_number = mapping["epics"][epic_key]["issue_number"]

        story_body = build_issue_body(content)

        issue_id, issue_number = create_issue(repo, token, story_title, story_body, [LABEL_NAME])
        print(f"Created story issue #{issue_number}: {story_title}")

        commit_sha = get_file_commit_sha(file_path)

        mapping["stories"][file_stem] = {
            "issue_id": issue_id,
            "issue_number": issue_number,
            "parent_epic": epic_key,
            "commit_sha": commit_sha,
        }

        if parent_epic_number:
            link_sub_issue(repo, token, parent_epic_number, issue_id)
            print(f"  Linked as sub-issue to epic #{parent_epic_number}")
        else:
            print(f"  WARNING: Parent epic '{epic_key}' not found in mapping. Story created but not linked.")

    return mapping


def main():
    repo = os.environ.get("GITHUB_REPOSITORY")
    token = os.environ.get("GITHUB_TOKEN")
    sync_all = os.environ.get("SYNC_ALL", "false").lower() == "true"

    if not repo or not token:
        print("ERROR: GITHUB_REPOSITORY and GITHUB_TOKEN must be set")
        sys.exit(1)

    mapping = load_mapping()

    if sync_all:
        from bmad_sync_lib import get_all_files
        all_files, _ = get_all_files()
        story_files = [f for f in all_files if "implementation-artifacts/" in f and f.endswith(".md")]
    else:
        from bmad_sync_lib import get_changed_files
        all_new, all_mod = get_changed_files()
        all_files = list(set(all_new + all_mod))
        story_files = [f for f in all_files if "implementation-artifacts/" in f and f.endswith(".md")]

    for file_path in story_files:
        content = Path(file_path).read_text()
        mapping = process_story(repo, token, mapping, file_path, content)

    save_mapping(mapping)

    committed = commit_file_to_git(str(Path("_bmad-output/.issue-mapping.json")), "BMad sync: update issue mapping")
    if committed:
        print("Committed mapping file to git.")
    else:
        print("WARNING: Could not commit mapping file. It may not have changed.")

    print(f"Stories sync completed. Processed {len(story_files)} story files.")


if __name__ == "__main__":
    main()