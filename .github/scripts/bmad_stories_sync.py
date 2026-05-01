#!/usr/bin/env python3
import os
import sys
from pathlib import Path

from bmad_sync_lib import (
    add_comment,
    build_issue_body,
    create_issue,
    ensure_label_exists,
    extract_epic_key,
    format_timestamp,
    get_file_commit_sha,
    get_file_commit_url,
    link_sub_issue,
    load_mapping,
    parse_story_title,
    save_mapping,
)

LABEL_NAME = "bmad"


def process_story_new(repo: str, token: str, mapping: dict, file_path: str, content: str) -> dict:
    file_stem = Path(file_path).stem

    if file_stem in mapping["stories"]:
        return mapping

    ensure_label_exists(repo, token)

    story_title = parse_story_title(content) or file_stem

    epic_key = extract_epic_key(content, file_stem)

    parent_epic_number = None
    epic_not_found_note = ""

    if epic_key and epic_key in mapping["epics"]:
        parent_epic_number = mapping["epics"][epic_key]["issue_number"]
    else:
        epic_not_found_note = "\n\n> **Note**: Parent epic not found in mapping. This story may not be linked to its epic yet. Run the epics sync or manually trigger the stories sync after epics are created."

    story_body = build_issue_body(content)
    story_body += epic_not_found_note

    issue_id, issue_number = create_issue(repo, token, story_title, story_body, [LABEL_NAME])

    commit_sha = get_file_commit_sha(file_path)
    commit_url = get_file_commit_url(file_path) if commit_sha else None

    mapping["stories"][file_stem] = {
        "issue_id": issue_id,
        "issue_number": issue_number,
        "parent_epic": epic_key,
        "commit_sha": commit_sha,
    }

    print(f"Created story issue #{issue_number}: {story_title}")

    if parent_epic_number:
        link_sub_issue(repo, token, parent_epic_number, issue_id)
        print(f"  Linked as sub-issue to epic #{parent_epic_number}")
    else:
        print(f"  WARNING: Parent epic '{epic_key}' not found in mapping. Story created but not linked.")

    return mapping


def process_story_modified(repo: str, token: str, mapping: dict, file_path: str, content: str) -> dict:
    file_stem = Path(file_path).stem

    if file_stem not in mapping["stories"]:
        return mapping

    story_info = mapping["stories"][file_stem]
    issue_number = story_info["issue_number"]

    story_title = parse_story_title(content) or file_stem
    repo_env = os.environ.get("GITHUB_REPOSITORY", "")
    commit_sha = get_file_commit_sha(file_path)
    commit_url = f"{repo_env}/commit/{commit_sha}" if commit_sha else None

    timestamp = format_timestamp()

    if commit_url:
        comment = f"""**BMad Source Changed** - {timestamp}

Story [{story_title}](https://github.com/{repo_env}/blob/main/{file_path}) was updated in BMAD source.

Commit: [{commit_sha}](https://github.com/{repo_env}/commit/{commit_sha})
"""
    else:
        comment = f"""**BMad Source Changed** - {timestamp}

Story [{story_title}](https://github.com/{repo_env}/blob/main/{file_path}) was updated in BMAD source.
"""

    add_comment(repo, token, issue_number, comment)

    if commit_sha:
        mapping["stories"][file_stem]["commit_sha"] = commit_sha

    print(f"Added modification comment to story issue #{issue_number}: {story_title}")

    return mapping


def get_story_files(new_files: list[str], modified_files: list[str]) -> tuple[list[str], list[str]]:
    new_stories = [f for f in new_files if "implementation-artifacts/" in f and f.endswith(".md")]
    mod_stories = [f for f in modified_files if "implementation-artifacts/" in f and f.endswith(".md")]
    return new_stories, mod_stories


def main():
    repo = os.environ.get("GITHUB_REPOSITORY")
    token = os.environ.get("GITHUB_TOKEN")
    sync_all = os.environ.get("SYNC_ALL", "false").lower() == "true"
    epics_workflow_status = os.environ.get("EPICS_WORKFLOW_STATUS", "")

    if not repo or not token:
        print("ERROR: GITHUB_REPOSITORY and GITHUB_TOKEN must be set")
        sys.exit(1)

    mapping = load_mapping()

    if sync_all:
        from bmad_sync_lib import get_all_files
        all_files, _ = get_all_files()
        new_files = [f for f in all_files if "implementation-artifacts/" in f]
        modified_files = []
    else:
        from bmad_sync_lib import get_changed_files
        all_new, all_mod = get_changed_files()
        new_files, modified_files = get_story_files(all_new, all_mod)

    if epics_workflow_status == "failure":
        print("WARNING: Epics workflow failed. Stories may not be linked to their epics correctly.")

    for file_path in new_files:
        content = Path(file_path).read_text()
        mapping = process_story_new(repo, token, mapping, file_path, content)

    for file_path in modified_files:
        content = Path(file_path).read_text()
        mapping = process_story_modified(repo, token, mapping, file_path, content)

    save_mapping(mapping)
    print(f"Stories sync completed. Processed {len(new_files)} new stories, {len(modified_files)} modified stories.")


if __name__ == "__main__":
    main()