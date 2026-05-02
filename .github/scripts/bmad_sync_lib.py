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


def find_issue_by_title(repo: str, token: str, title: str) -> dict | None:
    url = f"https://api.github.com/repos/{repo}/issues?state=all&per_page=100"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
    }
    request = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(request) as response:
            issues = json.loads(response.read())
            for issue in issues:
                if issue.get("title") == title:
                    return {"id": issue["id"], "number": issue["number"], "state": issue["state"]}
    except urllib.error.HTTPError:
        pass
    return None


def update_issue(repo: str, token: str, issue_number: int, body: str) -> None:
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"body": body}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="PATCH")
    with urllib.request.urlopen(request):
        pass


def format_timestamp() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def is_story_implemented(content: str) -> bool:
    lower = content.lower()
    if "story implemented" in lower:
        return True
    if re.search(r"story\s+\d+[.-]\d+\s+implemented", lower):
        return True
    all_checklist_complete = "implementation checklist" in lower and _all_implementation_items_checked(content)
    has_completion_notes = "### completion notes" in lower or "## completion notes" in lower or "completion notes list" in lower
    if all_checklist_complete and has_completion_notes:
        return True
    if has_completion_notes and _has_meaningful_completion_notes(content):
        return True
    return False


def _has_meaningful_completion_notes(content: str) -> bool:
    completion_pattern = re.compile(r"(?:###|##)\s+Completion Notes List\s*\n(.*?)(?=\n(?:##|###)|\Z)", re.DOTALL | re.IGNORECASE)
    match = completion_pattern.search(content)
    if not match:
        completion_pattern2 = re.compile(r"Completion Notes List\s*\n(.*)", re.DOTALL | re.IGNORECASE)
        match2 = completion_pattern2.search(content)
        if match2:
            notes_text = match2.group(1)
        else:
            return False
    else:
        notes_text = match.group(1)
    bullet_points = re.findall(r"^\s*[-*]\s+\w", notes_text, re.MULTILINE)
    return len(bullet_points) >= 1


def _all_implementation_items_checked(content: str) -> bool:
    checklist_section = re.search(r"##\s+implementation checklist\s*\n([\s\S]*?)(?=\n##|\Z)", content, re.IGNORECASE)
    if not checklist_section:
        checklist_section = re.search(r"###\s+implementation checklist\s*\n([\s\S]*?)(?=\n###|\n##|\Z)", content, re.IGNORECASE)
    if not checklist_section:
        return False
    checklist_text = checklist_section.group(1)
    unchecked = re.findall(r"- \[\s\]", checklist_text)
    return len(unchecked) == 0


def get_commit_author(file_path: str, repo_path: Path | None = None) -> str | None:
    if repo_path is None:
        repo_path = Path(__file__).parent.parent.parent

    result = subprocess.run(
        ["git", "log", "-1", "--format=%an", "--", file_path],
        capture_output=True,
        text=True,
        cwd=repo_path,
    )
    if result.returncode == 0 and result.stdout.strip():
        author = result.stdout.strip()
        if author and not author.startswith("%"):
            return author
    return os.environ.get("GITHUB_ACTOR")


def get_pr_for_commit(repo: str, token: str, sha: str) -> dict | None:
    url = f"https://api.github.com/repos/{repo}/commits/{sha}/pulls"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
    }
    request = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(request) as response:
            prs = json.loads(response.read())
            if prs:
                return {"number": prs[0]["number"], "title": prs[0]["title"], "body": prs[0]["body"] or ""}
    except urllib.error.HTTPError:
        pass
    return None


def link_pr_to_issue(repo: str, token: str, pr_number: int, issue_number: int) -> bool:
    print(f"WARNING: link_pr_to_issue API is deprecated, skipping PR-to-issue linking")
    return False


def update_comment(repo: str, token: str, comment_id: int, body: str) -> None:
    url = f"https://api.github.com/repos/{repo}/issues/comments/{comment_id}"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"body": body}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="PATCH")
    with urllib.request.urlopen(request):
        pass


def get_comments(repo: str, token: str, issue_number: int) -> list[dict]:
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/comments"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
    }
    request = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(request) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError:
        return []


def find_delivery_comment(comments: list[dict]) -> dict | None:
    for comment in comments:
        if "Story Delivered" in comment.get("body", ""):
            return comment
    return None


def update_pr_body(repo: str, token: str, pr_number: int, body_addition: str) -> None:
    existing_pr = None
    url = f"https://api.github.com/repos/{repo}/pulls/{pr_number}"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    request = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(request) as response:
            existing_pr = json.loads(response.read())
    except urllib.error.HTTPError:
        pass

    if existing_pr:
        current_body = existing_pr.get("body") or ""
        if body_addition not in current_body:
            new_body = f"{current_body}\n\n{body_addition}".strip()
            data = json.dumps({"body": new_body}).encode()
            patch_request = urllib.request.Request(url, data=data, headers=headers, method="PATCH")
            with urllib.request.urlopen(patch_request):
                pass


def close_issue(repo: str, token: str, issue_number: int) -> None:
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"state": "closed"}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="PATCH")
    with urllib.request.urlopen(request):
        pass


def assign_issue(repo: str, token: str, issue_number: int, assignee: str) -> None:
    url = f"https://api.github.com/repos/{repo}/issues/{issue_number}/assignees"
    headers = {
        "Authorization": f"token {token}",
        "Accept": "application/vnd.github.v3+json",
        "Content-Type": "application/json",
    }
    data = json.dumps({"assignees": [assignee]}).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(request):
            pass
    except urllib.error.HTTPError as e:
        if e.code == 422:
            pass
        else:
            raise


def get_issue_from_mapping(mapping: dict, file_stem: str) -> int | None:
    if file_stem in mapping.get("stories", {}):
        return mapping["stories"][file_stem].get("issue_number")
    return None


def is_direct_push_to_main() -> bool:
    result = subprocess.run(
        ["git", "rev-parse", "--abbrev-ref", "HEAD"],
        capture_output=True,
        text=True,
    )
    if result.returncode == 0 and result.stdout.strip() in ("main", "master"):
        return True
    return False


def commit_file_to_git(file_path: str, message: str, repo_path: Path | None = None) -> bool:
    if repo_path is None:
        repo_path = Path(__file__).parent.parent.parent
    try:
        subprocess.run(["git", "add", "-f", file_path], capture_output=True, cwd=repo_path, check=True)
        subprocess.run(
            ["git", "commit", "-m", message],
            capture_output=True,
            cwd=repo_path,
            env={**os.environ, "GIT_AUTHOR_NAME": "BMad Bot", "GIT_AUTHOR_EMAIL": "bmad-bot@github.com", "GIT_COMMITTER_NAME": "BMad Bot", "GIT_COMMITTER_EMAIL": "bmad-bot@github.com"},
        )
        return True
    except subprocess.CalledProcessError:
        return False