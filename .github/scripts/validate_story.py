#!/usr/bin/env python3
"""
Validate story implementation status.

Usage:
    python3 .github/scripts/validate_story.py _bmad-output/implementation-artifacts/2-2-allow-list-redaction.md
    python3 .github/scripts/validate_story.py 2-2-allow-list-redaction
    python3 .github/scripts/validate_story.py 2-2
    python3 .github/scripts/validate_story.py  # validates all stories
"""
import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / ".github" / "scripts"))

from bmad_sync_lib import (
    is_story_implemented,
    _all_implementation_items_checked,
    _has_meaningful_completion_notes,
    load_mapping,
    get_issue_from_mapping,
)


def find_story_file(identifier: str) -> Path | None:
    """Find story file by ID, key, or full path."""
    base = Path("_bmad-output/implementation-artifacts")

    path = Path(identifier)
    if path.exists():
        return path

    if not identifier.startswith("_bmad-output"):
        path = base / f"{identifier}.md"
        if path.exists():
            return path

    for suffix in ["", "-allow-list-redaction", "-custom-redaction-patterns", "-in-memory-only", "-redaction-alerts"]:
        potential = base / f"{identifier}{suffix}.md"
        if potential.exists():
            return potential

    patterns = [
        f"*{identifier}*.md",
        f"*-{identifier}-*.md",
    ]
    for pattern in patterns:
        matches = list(base.glob(pattern))
        if matches:
            return matches[0]

    return None


def validate_story(file_path: Path, verbose: bool = False) -> dict:
    """Validate a single story file."""
    content = file_path.read_text()

    has_checklist = "implementation checklist" in content.lower() or "tasks/subtasks" in content.lower()
    checklist_complete = _all_implementation_items_checked(content) if has_checklist else False
    has_completion_notes = "### completion notes" in content.lower() or "## completion notes" in content.lower()
    completion_meaningful = _has_meaningful_completion_notes(content) if has_completion_notes else False
    is_implemented = is_story_implemented(content)

    file_stem = file_path.stem
    mapping = load_mapping()
    issue_number = get_issue_from_mapping(mapping, file_stem)

    result = {
        "file": str(file_path),
        "stem": file_stem,
        "issue": issue_number,
        "implemented": is_implemented,
        "checklist": {
            "exists": has_checklist,
            "complete": checklist_complete,
        },
        "completion_notes": {
            "exists": has_completion_notes,
            "meaningful": completion_meaningful,
        },
    }

    if verbose:
        status_match = None
        for line in content.split("\n"):
            stripped = line.strip()
            if stripped.lower().startswith("status:") or stripped.lower().startswith("## status"):
                status_match = stripped.split(":", 1)[-1].strip()
                if status_match.startswith("##"):
                    status_match = status_match.lstrip("#").strip()
                break
        result["status"] = status_match or "unknown"

    return result


def format_result(data: dict, verbose: bool = False) -> str:
    """Format validation result for display."""
    stem = data["stem"]
    impl = "✅ IMPLEMENTED" if data["implemented"] else "❌ NOT IMPLEMENTED"

    checklist_emoji = "✅" if data["checklist"]["complete"] else "⬜" if data["checklist"]["exists"] else "❌"
    notes_emoji = "✅" if data["completion_notes"]["meaningful"] else "⬜" if data["completion_notes"]["exists"] else "❌"

    lines = [
        f"{impl} {stem}",
        f"  {checklist_emoji} Checklist: {'complete' if data['checklist']['complete'] else ('present' if data['checklist']['exists'] else 'missing')}",
        f"  {notes_emoji} Completion Notes: {'meaningful' if data['completion_notes']['meaningful'] else ('present' if data['completion_notes']['exists'] else 'missing')}",
    ]

    if data.get("issue"):
        lines.insert(2, f"  📋 GitHub Issue: #{data['issue']}")

    if verbose and data.get("status"):
        lines.insert(1, f"  📌 Status: {data['status']}")

    issues = []
    if data["checklist"]["exists"] and not data["checklist"]["complete"]:
        issues.append("checklist incomplete")
    if not data["completion_notes"]["exists"]:
        issues.append("no completion notes")
    elif data["completion_notes"]["exists"] and not data["completion_notes"]["meaningful"]:
        issues.append("completion notes not meaningful")

    if issues:
        lines.append(f"  ⚠️  Issues: {', '.join(issues)}")

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Validate story implementation status")
    parser.add_argument("story", nargs="?", help="Story file, key, or ID (e.g., 2-2-allow-list-redaction, 2-2)")
    parser.add_argument("-v", "--verbose", action="store_true", help="Show verbose output")
    parser.add_argument("-a", "--all", action="store_true", help="Validate all stories")
    args = parser.parse_args()

    if not args.story and not args.all:
        parser.print_help()
        return

    if args.all:
        base = Path("_bmad-output/implementation-artifacts")
        stories = sorted(base.glob("*.md"), key=lambda p: p.name)
    else:
        story_file = find_story_file(args.story)
        if not story_file:
            print(f"❌ Story not found: {args.story}")
            sys.exit(1)
        stories = [story_file]

    results = []
    for story in stories:
        result = validate_story(story, verbose=args.verbose)
        results.append(result)

    print(f"=== Story Validation ({len(results)} story{'s' if len(results) > 1 else ''}) ===\n")

    for result in results:
        print(format_result(result, verbose=args.verbose))
        print()

    implemented_count = sum(1 for r in results if r["implemented"])
    print(f"Summary: {implemented_count}/{len(results)} stories implemented")


if __name__ == "__main__":
    main()