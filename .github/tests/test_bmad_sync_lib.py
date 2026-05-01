import json
import os
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from bmad_sync_lib import (
    add_comment,
    build_issue_body,
    create_issue,
    ensure_label_exists,
    extract_epic_key,
    format_timestamp,
    get_file_commit_sha,
    is_step_04_completed,
    link_sub_issue,
    load_mapping,
    parse_frontmatter,
    parse_story_title,
    save_mapping,
)


FIXTURES_DIR = Path(__file__).parent / "fixtures"


class TestParseFrontmatter:
    def test_valid_yaml_frontmatter(self):
        content = """---
key: value
list:
  - item1
  - item2
---
# Title
Content"""
        result = parse_frontmatter(content)
        assert result == {"key": "value", "list": ["item1", "item2"]}

    def test_missing_frontmatter(self):
        content = "# Title\nContent without frontmatter"
        result = parse_frontmatter(content)
        assert result == {}

    def test_empty_frontmatter(self):
        content = """---
---
# Title"""
        result = parse_frontmatter(content)
        assert result == {}

    def test_invalid_yaml(self):
        content = """---
key: value
  invalid: indentation
---
# Title"""
        result = parse_frontmatter(content)
        assert result == {}


class TestParseStoryTitle:
    def test_standard_title(self):
        content = "# Story 1-1: Initialize Test Project\n## Story Requirements\n..."
        result = parse_story_title(content)
        assert result == "Initialize Test Project"

    def test_title_with_em_dash(self):
        content = "# Story 2-1 – Implement Auth\n## Story Requirements\n..."
        result = parse_story_title(content)
        assert result == "Implement Auth"

    def test_title_with_colon(self):
        content = "# Story 3-1: Add Feature X\n## Story Requirements\n..."
        result = parse_story_title(content)
        assert result == "Add Feature X"

    def test_no_story_header(self):
        content = "# Some Other Title\n## Section\n..."
        result = parse_story_title(content)
        assert result is None

    def test_empty_content(self):
        result = parse_story_title("")
        assert result is None


class TestExtractEpicKey:
    def test_from_frontmatter_epic_1(self):
        content = """---
epic: epic-1
title: Test Story
---
# Story 1-1: Test"""
        result = extract_epic_key(content, "1-1-test-story.md")
        assert result == "1"

    def test_from_frontmatter_epic_2(self):
        content = """---
epic: epic-2
title: Test Story
---
# Story 2-1: Test"""
        result = extract_epic_key(content, "2-1-test-story.md")
        assert result == "2"

    def test_fallback_to_filename(self):
        content = """---
title: No Epic Field
---
# Story 3-1: Test"""
        result = extract_epic_key(content, "3-1-no-frontmatter.md")
        assert result == "3"

    def test_frontmatter_takes_priority(self):
        content = """---
epic: epic-5
title: Test
---
# Story 2-1: Test"""
        result = extract_epic_key(content, "2-1-test-story.md")
        assert result == "5"

    def test_no_epic_info(self):
        content = "# Story 4-1: Test without epic info"
        result = extract_epic_key(content, "4-1-test.md")
        assert result == "4"


class TestIsStep04Completed:
    def test_step_04_completed(self):
        content = """---
stepsCompleted: [step-01, step-02, step-03, step-04-final-validation]
---
# Epic"""
        assert is_step_04_completed(content) is True

    def test_step_04_not_completed(self):
        content = """---
stepsCompleted: [step-01, step-02, step-03]
---
# Epic"""
        assert is_step_04_completed(content) is False

    def test_no_steps_defined(self):
        content = """---
title: Test
---
# Epic"""
        assert is_step_04_completed(content) is False

    def test_empty_steps_list(self):
        content = """---
stepsCompleted: []
---
# Epic"""
        assert is_step_04_completed(content) is False


class TestBuildIssueBody:
    def test_removes_frontmatter(self):
        content = """---
title: Test
---
# Story Title

## Section
Content"""
        result = build_issue_body(content)
        assert "---" not in result
        assert "# Story Title" in result
        assert "## Section" in result

    def test_preserves_content(self):
        content = "# Story Title\n\n## Requirements\n\nSome content"
        result = build_issue_body(content)
        assert "Story Title" in result
        assert "Requirements" in result
        assert "Some content" in result


class TestGetFileCommitSha:
    def test_returns_none_for_new_file(self, tmp_path):
        result = get_file_commit_sha("nonexistent.md", tmp_path)
        assert result is None

    @patch("subprocess.run")
    def test_returns_short_sha(self, mock_run, tmp_path):
        mock_run.return_value = MagicMock(returncode=0, stdout="abc1234\n")
        result = get_file_commit_sha("test.md", tmp_path)
        assert result == "abc1234"


class TestFormatTimestamp:
    def test_returns_iso_format(self):
        result = format_timestamp()
        assert result.endswith("Z")
        assert "T" in result


class TestLoadMapping:
    def test_load_existing_mapping(self, tmp_path):
        import bmad_sync_lib
        mapping_file = tmp_path / ".issue-mapping.json"
        test_data = {"last_sync": "2026-05-01T12:00:00Z", "epics": {"1": {"issue_id": 123}}, "stories": {}}
        mapping_file.write_text(json.dumps(test_data))

        with patch.object(bmad_sync_lib, "MAPPING_FILE", mapping_file):
            result = bmad_sync_lib.load_mapping()

        assert result["last_sync"] == "2026-05-01T12:00:00Z"
        assert result["epics"]["1"]["issue_id"] == 123

    def test_load_nonexistent_mapping(self, tmp_path):
        import bmad_sync_lib
        mapping_file = tmp_path / ".issue-mapping.json"

        with patch.object(bmad_sync_lib, "MAPPING_FILE", mapping_file):
            result = bmad_sync_lib.load_mapping()

        assert result["last_sync"] is None
        assert result["epics"] == {}
        assert result["stories"] == {}

    def test_returns_default_for_missing_file(self, tmp_path):
        os.chdir(tmp_path)
        result = load_mapping()
        assert result == {"last_sync": None, "epics": {}, "stories": {}}


class TestSaveMapping:
    def test_saves_mapping_with_timestamp(self, tmp_path, monkeypatch):
        import bmad_sync_lib
        mapping_file = tmp_path / ".issue-mapping.json"

        with patch.object(bmad_sync_lib, "MAPPING_FILE", mapping_file):
            test_mapping = {"epics": {}, "stories": {}}
            bmad_sync_lib.save_mapping(test_mapping)

        assert mapping_file.exists()
        saved = json.loads(mapping_file.read_text())
        assert saved["last_sync"] is not None