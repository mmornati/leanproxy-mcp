#!/usr/bin/env python3
import json
import os
import re
import subprocess
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import yaml

BMAD_OUTPUT_DIR = Path("_bmad-output")
MAPPING_FILE = BMAD_OUTPUT_DIR / ".issue-mapping.json"
LABEL_NAME = "bmad"


def parse_frontmatter(content: str) -> dict[str, Any]:
    frontmatter_pattern = re.compile(r"^---\s*\n(.*?)\n---\s*\n", re.DOTALL)
    match = frontmatter_pattern.match(content)
    if not match:
        return {}
    try:
        return yaml.safe_load(match.group(1)) or {}
    except yaml.YAMLError:
        return {}


def parse_story_title(content: str) -> str | None:
    story_title_pattern = re.compile(r"^#\s+Story\s+\d+-\d+[\s:–-]*(.+)", re.IGNORECASE)
    for line in content.split("\n"):
        match = story_title_pattern.match(line.strip())
        if match:
            return match.group(1).strip()
    return None


def extract_epic_key(content: str, filename: str) -> str | None:
    frontmatter = parse_frontmatter(content)
    if frontmatter.get("epic"):
        epic_value = frontmatter["epic"]
        epic_match = re.match(r"epic[-_]?(\d+)", epic_value, re.IGNORECASE)
        if epic_match:
            return epic_match.group(1)

    filename_pattern = re.compile(r"^(\d+)-\d+-")
    match = filename_pattern.match(filename)
    if match:
        return match.group(1)
    return None


def get_file_commit_sha(file_path: str, repo_path: Path | None = None) -> str | None:
    if repo_path is None:
        repo_path = Path(__file__).parent.parent.parent

    result = subprocess.run(
        ["git", "log", "-1", "--format=%h", "--", file_path],
        capture_output=True,
        text=True,
        cwd=repo_path,
    )
    if result.returncode == 0 and result.stdout.strip():
        return result.stdout.strip()
    return None


def get_file_commit_url(file_path: str, repo_path: Path | None = None) -> str | None:
    sha = get_file_commit_sha(file_path, repo_path)
    if not sha:
        return None
    repo = os.environ.get("GITHUB_REPOSITORY")
    if not repo:
        return None
    return f"https://github.com/{repo}/commit/{sha}"


def build_issue_body(content: str, file_path: str | None = None) -> str:
    body = content.strip()

    frontmatter_pattern = re.compile(r"^---\s*\n.*?\n---\s*\n", re.DOTALL)
    body = frontmatter_pattern.sub("", body)

    body = body.strip()
    return body


def load_mapping() -> dict[str, Any]:
    if MAPPING_FILE.exists():
        with open(MAPPING_FILE, "r") as f:
            return json.load(f)
    return {"last_sync": None, "epics": {}, "stories": {}}


def save_mapping(mapping: dict[str, Any]) -> None:
    mapping["last_sync"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    with open(MAPPING_FILE, "w") as f:
        json.dump(mapping, f, indent=2)


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

    create_url = f"https://api.github.com/repos/{repo}/labels"
    data = json.dumps({"name": LABEL_NAME, "color": "FF5722", "description": "BMad managed issue"}).encode()
    request = urllib.request.Request(create_url, data=data, headers=headers, method="POST")
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


def get_changed_files(base_dir: Path | None = None) -> tuple[list[str], list[str]]:
    if base_dir is None:
        base_dir = Path(__file__).parent.parent.parent

    result = subprocess.run(
        ["git", "diff", "--name-only", "HEAD~1", "HEAD"],
        capture_output=True,
        text=True,
        cwd=base_dir,
    )
    current_files = [f.strip() for f in result.stdout.strip().split("\n") if f.strip()]

    result = subprocess.run(
        ["git", "diff", "--name-only", "--cached", "HEAD"],
        capture_output=True,
        text=True,
        cwd=base_dir,
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
            cwd=base_dir,
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


def is_step_04_completed(content: str) -> bool:
    frontmatter = parse_frontmatter(content)
    steps = frontmatter.get("stepsCompleted", [])
    if isinstance(steps, str):
        steps = [s.strip() for s in steps.split(",")]
    return "step-04-final-validation" in steps


def slugify(text: str) -> str:
    text = text.lower()
    text = re.sub(r"[^\w\s-]", "", text)
    text = re.sub(r"[\s_]+", "-", text)
    text = re.sub(r"-+", "-", text)
    return text.strip("-")


def format_timestamp() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")