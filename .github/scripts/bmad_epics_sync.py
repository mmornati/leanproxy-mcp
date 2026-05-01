#!/usr/bin/env python3
import os
import re
import sys
from pathlib import Path

from bmad_sync_lib import (
    build_issue_body,
    commit_file_to_git,
    create_issue,
    ensure_label_exists,
    find_issue_by_title,
    format_timestamp,
    is_step_04_completed,
    link_sub_issue,
    load_mapping,
    parse_frontmatter,
    save_mapping,
)

LABEL_NAME = "bmad"


def parse_epics_md(content: str) -> list[dict]:
    epics = []
    current_epic = None

    lines = content.split("\n")
    epic_pattern = re.compile(r"^#+\s*Epic\s+(\d+)[\s:–-]*(.+)", re.IGNORECASE)
    story_pattern = re.compile(r"^#+\s*\d+\.\d+\s+(.+)")

    for line in lines:
        epic_match = epic_pattern.match(line)
        if epic_match:
            if current_epic:
                epics.append(current_epic)
            epic_num = epic_match.group(1)
            epic_title = epic_match.group(2).strip()
            current_epic = {
                "key": epic_num,
                "title": epic_title,
                "stories": [],
            }
            continue

        if current_epic:
            story_match = story_pattern.match(line)
            if story_match:
                story_title = story_match.group(1).strip()
                current_epic["stories"].append({
                    "title": story_title,
                })

    if current_epic:
        epics.append(current_epic)

    return epics


def process_epic(repo: str, token: str, mapping: dict, epic: dict, content: str) -> dict:
    epic_key = epic["key"]
    epic_title = f"Epic {epic_key}: {epic['title']}"

    existing_issue = find_issue_by_title(repo, token, epic_title)

    if existing_issue:
        issue_number = existing_issue["number"]
        mapping["epics"][epic_key] = {
            "issue_id": existing_issue["id"],
            "issue_number": issue_number,
            "title": epic["title"],
        }
        print(f"Epic issue #{issue_number} already exists: {epic_title}")
        return mapping

    if not is_step_04_completed(content):
        print(f"Skipping epic {epic_key}: step-04-final-validation not completed")
        return mapping

    ensure_label_exists(repo, token)
    epic_body = build_issue_body(content)
    issue_id, issue_number = create_issue(repo, token, epic_title, epic_body, [LABEL_NAME])

    mapping["epics"][epic_key] = {
        "issue_id": issue_id,
        "issue_number": issue_number,
        "title": epic["title"],
    }
    print(f"Created epic issue #{issue_number}: {epic_title}")

    return mapping


def main():
    repo = os.environ.get("GITHUB_REPOSITORY")
    token = os.environ.get("GITHUB_TOKEN")
    sync_all = os.environ.get("SYNC_ALL", "false").lower() == "true"

    if not repo or not token:
        print("ERROR: GITHUB_REPOSITORY and GITHUB_TOKEN must be set")
        sys.exit(1)

    mapping = load_mapping()

    file_path = "_bmad-output/planning-artifacts/epics.md"
    if not os.path.exists(file_path):
        print(f"ERROR: {file_path} not found")
        sys.exit(1)

    content = Path(file_path).read_text()
    epics = parse_epics_md(content)

    for epic in epics:
        mapping = process_epic(repo, token, mapping, epic, content)

    save_mapping(mapping)

    committed = commit_file_to_git(str(Path("_bmad-output/.issue-mapping.json")), "BMad epics sync: update issue mapping")
    if committed:
        print("Committed mapping file to git.")
    else:
        print("WARNING: Could not commit mapping file. It may not have changed.")

    print(f"Epics sync completed. Processed {len(epics)} epics.")


if __name__ == "__main__":
    main()