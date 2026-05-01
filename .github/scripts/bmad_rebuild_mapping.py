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
    get_file_commit_sha,
    is_step_04_completed,
    link_sub_issue,
    load_mapping,
    parse_frontmatter,
    parse_story_title,
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
            current_epic = {"key": epic_num, "title": epic_title, "stories": []}
            continue
        if current_epic:
            story_match = story_pattern.match(line)
            if story_match:
                current_epic["stories"].append({"title": story_match.group(1).strip()})
    if current_epic:
        epics.append(current_epic)
    return epics


def rebuild_mapping(repo: str, token: str, mapping: dict) -> dict:
    mapping = {"last_sync": None, "epics": {}, "stories": {}}

    epics_file = Path("_bmad-output/planning-artifacts/epics.md")
    if epics_file.exists():
        content = epics_file.read_text()
        epics = parse_epics_md(content)
        ensure_label_exists(repo, token)

        for epic in epics:
            epic_key = epic["key"]
            epic_title = f"Epic {epic_key}: {epic['title']}"

            existing = find_issue_by_title(repo, token, epic_title)
            if existing:
                mapping["epics"][epic_key] = {
                    "issue_id": existing["id"],
                    "issue_number": existing["number"],
                    "title": epic["title"],
                }
                print(f"Found epic issue #{existing['number']}: {epic_title}")
            elif is_step_04_completed(content):
                epic_body = build_issue_body(content)
                issue_id, issue_number = create_issue(repo, token, epic_title, epic_body, [LABEL_NAME])
                mapping["epics"][epic_key] = {
                    "issue_id": issue_id,
                    "issue_number": issue_number,
                    "title": epic["title"],
                }
                print(f"Created epic issue #{issue_number}: {epic_title}")

    story_files = list(Path("_bmad-output/implementation-artifacts").glob("*.md"))
    ensure_label_exists(repo, token)

    for story_file in story_files:
        file_stem = story_file.stem
        content = story_file.read_text()

        story_title = parse_story_title(content) or file_stem
        epic_key = None
        frontmatter = parse_frontmatter(content)
        if frontmatter.get("epic"):
            epic_match = re.match(r"epic[-_]?(\d+)", frontmatter["epic"], re.IGNORECASE)
            if epic_match:
                epic_key = epic_match.group(1)
        if not epic_key:
            filename_match = re.match(r"^(\d+)-\d+-", file_stem)
            if filename_match:
                epic_key = filename_match.group(1)

        existing = find_issue_by_title(repo, token, story_title)
        if existing:
            mapping["stories"][file_stem] = {
                "issue_id": existing["id"],
                "issue_number": existing["number"],
                "parent_epic": epic_key,
            }
            print(f"Found story issue #{existing['number']}: {story_title}")

            if epic_key and epic_key in mapping["epics"]:
                parent_number = mapping["epics"][epic_key]["issue_number"]
                try:
                    link_sub_issue(repo, token, parent_number, existing["id"])
                    print(f"  Linked to epic #{parent_number}")
                except Exception as e:
                    print(f"  Could not link to epic: {e}")
        else:
            story_body = build_issue_body(content)
            issue_id, issue_number = create_issue(repo, token, story_title, story_body, [LABEL_NAME])
            commit_sha = get_file_commit_sha(str(story_file))

            mapping["stories"][file_stem] = {
                "issue_id": issue_id,
                "issue_number": issue_number,
                "parent_epic": epic_key,
                "commit_sha": commit_sha,
            }
            print(f"Created story issue #{issue_number}: {story_title}")

            if epic_key and epic_key in mapping["epics"]:
                parent_number = mapping["epics"][epic_key]["issue_number"]
                try:
                    link_sub_issue(repo, token, parent_number, issue_id)
                    print(f"  Linked to epic #{parent_number}")
                except Exception as e:
                    print(f"  Could not link to epic: {e}")

    return mapping


def main():
    repo = os.environ.get("GITHUB_REPOSITORY")
    token = os.environ.get("GITHUB_TOKEN")

    if not repo or not token:
        print("ERROR: GITHUB_REPOSITORY and GITHUB_TOKEN must be set")
        sys.exit(1)

    mapping = load_mapping()
    mapping = rebuild_mapping(repo, token, mapping)
    save_mapping(mapping)

    committed = commit_file_to_git(
        str(Path("_bmad-output/.issue-mapping.json")),
        "BMad rebuild: synchronized issue mapping with GitHub"
    )
    if committed:
        print("Committed mapping file to git.")
    else:
        print("WARNING: Could not commit mapping file.")

    print(f"Rebuild completed. {len(mapping['epics'])} epics, {len(mapping['stories'])} stories.")


if __name__ == "__main__":
    main()