#!/usr/bin/env python3
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import yaml

BMAD_OUTPUT_DIR = Path("_bmad-output")
MAPPING_FILE = BMAD_OUTPUT_DIR / ".issue-mapping.json"
LABEL_NAME = "bmad"


def load_mapping() -> dict[str, Any]:
    if MAPPING_FILE.exists():
        with open(MAPPING_FILE, "r") as f:
            return json.load(f)
    return {"last_sync": None, "epics": {}, "stories": {}}


def save_mapping(mapping: dict[str, Any]) -> None:
    mapping["last_sync"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    with open(MAPPING_FILE, "w") as f:
        json.dump(mapping, f, indent=2)


def get_changed_files() -> tuple[list[str], list[str]]:
    result = subprocess.run(
        ["git", "diff", "--name-only", "HEAD~1", "HEAD"],
        capture_output=True,
        text=True,
        cwd=Path(__file__).parent.parent.parent,
    )
    current_files = [f.strip() for f in result.stdout.strip().split("\n") if f.strip()]

    result = subprocess.run(
        ["git", "diff", "--name-only", "--cached", "HEAD"],
        capture_output=True,
        text=True,
        cwd=Path(__file__).parent.parent.parent,
    )
    staged_files = [f.strip() for f in result.stdout.strip().split("\n") if f.strip()]

    all_changed = set(current_files + staged_files)
    new_files = set()
    modified_files = set()

    for f in all_changed:
        if not f.startswith("_bmad-output/"):
            continue
        if not f.endswith(".md"):
            continue
        result = subprocess.run(
            ["git", "log", "-1", "--format=", "--", f],
            capture_output=True,
            text=True,
            cwd=Path(__file__).parent.parent.parent,
        )
        if result.returncode != 0:
            new_files.add(f)
        else:
            modified_files.add(f)

    return list(new_files), list(modified_files)


def get_all_files() -> tuple[list[str], list[str]]:
    all_files = []
    for f in BMAD_OUTPUT_DIR.rglob("*.md"):
        file_path = str(f)
        if file_path.startswith("_bmad-output/"):
            all_files.append(file_path)
    return all_files, []


def parse_epics_md(content: str) -> list[dict[str, Any]]:
    epics = []
    current_epic = None

    lines = content.split("\n")
    epic_pattern = re.compile(r"^#+\s*Epic\s+(\d+)[\s:–-]*(.+)", re.IGNORECASE)
    story_pattern = re.compile(r"^#+\s*\d+\.\d+\s+(.+)")
    epic_header_pattern = re.compile(r"^#+\s+(.+?)\s+Epic", re.IGNORECASE)

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

        if current_epic is None:
            header_match = epic_header_pattern.match(line)
            if header_match and "epic" in line.lower():
                current_epic = {
                    "key": header_match.group(1).strip(),
                    "title": header_match.group(2).strip() if header_match.lastindex >= 2 else header_match.group(1).strip(),
                    "stories": [],
                }
                continue

        if current_epic:
            story_match = story_pattern.match(line)
            if story_match:
                story_title = story_match.group(1).strip()
                story_key = f"{current_epic['key']}-{len(current_epic['stories']) + 1}-{slugify(story_title)}"
                current_epic["stories"].append({
                    "key": story_key,
                    "title": story_title,
                })

    if current_epic:
        epics.append(current_epic)

    return epics


def slugify(text: str) -> str:
    text = text.lower()
    text = re.sub(r"[^\w\s-]", "", text)
    text = re.sub(r"[\s_]+", "-", text)
    text = re.sub(r"-+", "-", text)
    return text.strip("-")


def ensure_label_exists(repo: str, token: str) -> None:
    url = f"https://api.github.com/repos/{repo}/labels/{LABEL_NAME}"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
    }

    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with urllib.request.urlopen(request) as response:
            response.read()
            return
    except urllib.error.HTTPError as e:
        if e.code != 404:
            return

    data = json.dumps({"name": LABEL_NAME, "color": "FF5722", "description": "BMad managed issue"}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request) as response:
            response.read()
    except urllib.error.HTTPError as e:
        if e.code == 422:
            pass
        else:
            raise


def create_issue(repo: str, token: str, title: str, body: str, labels: list[str]) -> tuple[int, int]:
    url = f"https://api.github.com/repos/{repo}/issues"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"title": title, "body": body, "labels": labels}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")

    with urllib.request.urlopen(request) as response:
        result = json.loads(response.read())
        return result["id"], result["number"]


def add_comment(repo: str, token: str, issue_number: int, body: str) -> None:
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"body": body}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")

    with urllib.request.urlopen(request):
        pass


def link_sub_issue(repo: str, token: str, parent_number: int, sub_issue_id: int) -> None:
    url = f"https://api.github.com/repos/{repo}/issues/{parent_number}/sub_issues"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"sub_issue_id": sub_issue_id}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")

    try:
        with urllib.request.urlopen(request) as response:
            response.read()
    except urllib.error.HTTPError as e:
        if e.code == 422:
            pass
        else:
            raise


def process_epics_new(repo: str, token: str, mapping: dict[str, Any], content: str) -> dict[str, Any]:
    epics = parse_epics_md(content)
    ensure_label_exists(repo, token)

    for epic in epics:
        epic_key = epic["key"]
        if epic_key in mapping["epics"]:
            continue

        epic_body = f"""## Epic: {epic['title']}

Generated by BMad sync workflow.
"""
        issue_id, issue_number = create_issue(repo, token, f"Epic {epic_key}: {epic['title']}", epic_body, [LABEL_NAME])
        mapping["epics"][epic_key] = {
            "issue_id": issue_id,
            "issue_number": issue_number,
            "title": epic["title"],
        }

        for story in epic["stories"]:
            story_body = f"""## Story: {story['title']}

Generated by BMad sync workflow.
"""
            story_issue_id, story_issue_number = create_issue(repo, token, story["key"], story_body, [LABEL_NAME])
            mapping["stories"][story["key"]] = {
                "issue_id": story_issue_id,
                "issue_number": story_issue_number,
                "parent_epic": epic_key,
            }

            link_sub_issue(repo, token, issue_number, story_issue_id)

    return mapping


def process_epics_modified(repo: str, token: str, mapping: dict[str, Any], content: str) -> dict[str, Any]:
    epics = parse_epics_md(content)

    for epic in epics:
        epic_key = epic["key"]
        if epic_key not in mapping["epics"]:
            continue

        issue_number = mapping["epics"][epic_key]["issue_number"]
        comment = f"""**BMad Sync Update** - {datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}

Epics file has been modified. Manual review may be needed.
"""
        add_comment(repo, token, issue_number, comment)

    return mapping


def process_story_new(repo: str, token: str, mapping: dict[str, Any], file_path: str, content: str) -> dict[str, Any]:
    file_name = Path(file_path).stem
    epic_match = re.match(r"(\d+)-(\d+)-", file_name)
    if not epic_match:
        return mapping

    epic_key = epic_match.group(1)
    if epic_key not in mapping["epics"]:
        return mapping

    if file_name in mapping["stories"]:
        return mapping

    parent_issue_number = mapping["epics"][epic_key]["issue_number"]

    story_body = f"""## Story File

Generated by BMad sync workflow.

File: `{file_path}`
"""
    issue_id, issue_number = create_issue(repo, token, file_name, story_body, [LABEL_NAME])
    mapping["stories"][file_name] = {
        "issue_id": issue_id,
        "issue_number": issue_number,
        "parent_epic": epic_key,
    }

    link_sub_issue(repo, token, parent_issue_number, issue_id)

    return mapping


def process_story_modified(repo: str, token: str, mapping: dict[str, Any], file_path: str, content: str) -> dict[str, Any]:
    file_name = Path(file_path).stem
    if file_name not in mapping["stories"]:
        return mapping

    issue_number = mapping["stories"][file_name]["issue_number"]
    comment = f"""**BMad Sync Update** - {datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}

Story file has been modified. Manual review may be needed.
"""
    add_comment(repo, token, issue_number, comment)

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
        all_files, _ = get_all_files()
        new_files, modified_files = all_files, []
    else:
        new_files, modified_files = get_changed_files()

    for file_path in new_files:
        if "planning-artifacts/epics.md" in file_path:
            content = Path(file_path).read_text()
            mapping = process_epics_new(repo, token, mapping, content)
        elif "implementation-artifacts/" in file_path and file_path.endswith(".md"):
            content = Path(file_path).read_text()
            mapping = process_story_new(repo, token, mapping, file_path, content)

    for file_path in modified_files:
        if "planning-artifacts/epics.md" in file_path:
            content = Path(file_path).read_text()
            mapping = process_epics_modified(repo, token, mapping, content)
        elif "implementation-artifacts/" in file_path and file_path.endswith(".md"):
            content = Path(file_path).read_text()
            mapping = process_story_modified(repo, token, mapping, file_path, content)

    save_mapping(mapping)
    print(f"BMad sync completed. Processed {len(new_files)} new files, {len(modified_files)} modified files.")


if __name__ == "__main__":
    main()