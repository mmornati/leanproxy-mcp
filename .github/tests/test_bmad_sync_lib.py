import json
import os
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from bmad_sync_lib import (
    add_comment,
    assign_issue,
    build_issue_body,
    close_issue,
    create_issue,
    ensure_label_exists,
    extract_epic_key,
    find_delivery_comment,
    format_timestamp,
    get_comments,
    get_commit_author,
    get_file_commit_sha,
    get_issue_from_mapping,
    get_pr_for_commit,
    is_direct_push_to_main,
    is_step_04_completed,
    is_story_implemented,
    link_pr_to_issue,
    link_sub_issue,
    load_mapping,
    parse_frontmatter,
    parse_story_title,
    save_mapping,
    update_comment,
    update_pr_body,
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


class TestIsStoryImplemented:
    def test_detects_story_implemented_marker(self):
        content = "## Change Log\n- 2026-05-01: Story 1.1 implemented"
        assert is_story_implemented(content) is True

    def test_detects_story_implemented_case_insensitive(self):
        content = "## Change Log\n- 2026-05-01: Story 2.1 IMPLEMENTED"
        assert is_story_implemented(content) is True

    def test_detects_plain_story_implemented(self):
        content = "## Change Log\n- 2026-05-01: Story 3.1 implemented — Added feature"
        assert is_story_implemented(content) is True

    def test_detects_all_checkboxes_checked_with_completion_notes(self):
        content = """## Implementation Checklist
- [x] Task 1
- [x] Task 2
### Completion Notes
All done!"""
        assert is_story_implemented(content) is True

    def test_no_marker_returns_false(self):
        content = "## Change Log\n- 2026-05-01: Story updated"
        assert is_story_implemented(content) is False

    def test_empty_content_returns_false(self):
        assert is_story_implemented("") is False

    def test_incomplete_checklist_returns_false(self):
        content = """## Implementation Checklist
- [x] Task 1
- [ ] Task 2
### Completion Notes
All done!"""
        assert is_story_implemented(content) is False

    def test_missing_completion_notes_returns_false(self):
        content = """## Implementation Checklist
- [x] Task 1
- [x] Task 2"""
        assert is_story_implemented(content) is False


class TestGetCommitAuthor:
    @patch("subprocess.run")
    def test_returns_author_name_from_git_log(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="John Doe\n")
        result = get_commit_author("test.md")
        assert result == "John Doe"

    @patch.dict(os.environ, {"GITHUB_ACTOR": "test-user"}, clear=False)
    @patch("subprocess.run")
    def test_returns_github_actor_on_error(self, mock_run):
        mock_run.return_value = MagicMock(returncode=1, stdout="")
        result = get_commit_author("test.md")
        assert result == "test-user"


class TestGetPrForCommit:
    @patch("urllib.request.urlopen")
    def test_returns_pr_info(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'[{"number": 42, "title": "Feature PR", "body": "Fixes #1"}]'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        result = get_pr_for_commit("owner/repo", "token", "abc123")
        assert result == {"number": 42, "title": "Feature PR", "body": "Fixes #1"}

    @patch("urllib.request.urlopen")
    def test_returns_none_when_no_prs(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'[]'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        result = get_pr_for_commit("owner/repo", "token", "abc123")
        assert result is None


class TestLinkPrToIssue:
    def test_returns_false_with_warning(self):
        result = link_pr_to_issue("owner/repo", "token", 42, 10)
        assert result is False


class TestGetComments:
    @patch("urllib.request.urlopen")
    def test_returns_comments_list(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'[{"id": 1, "body": "Test comment"}]'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        result = get_comments("owner/repo", "token", 10)
        assert len(result) == 1
        assert result[0]["id"] == 1

    @patch("urllib.request.urlopen")
    def test_returns_empty_on_error(self, mock_urlopen):
        import urllib.error

        mock_urlopen.side_effect = urllib.error.HTTPError(url="", code=404, msg="", hdrs={}, fp=None)
        result = get_comments("owner/repo", "token", 10)
        assert result == []


class TestFindDeliveryComment:
    def test_finds_existing_delivery_comment(self):
        comments = [
            {"id": 1, "body": "Some other comment"},
            {"id": 2, "body": "**Story Delivered** via PR #42"},
            {"id": 3, "body": "Another comment"},
        ]
        result = find_delivery_comment(comments)
        assert result["id"] == 2

    def test_returns_none_when_not_found(self):
        comments = [
            {"id": 1, "body": "Some other comment"},
        ]
        result = find_delivery_comment(comments)
        assert result is None

    def test_returns_none_for_empty_list(self):
        result = find_delivery_comment([])
        assert result is None


class TestUpdateComment:
    @patch("urllib.request.urlopen")
    def test_updates_comment(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        update_comment("owner/repo", "token", 100, "Updated body")
        mock_urlopen.assert_called_once()


class TestUpdatePrBody:
    @patch("urllib.request.urlopen")
    def test_updates_pr_body_with_closes_marker(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'{"body": "Existing body"}'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        update_pr_body("owner/repo", "token", 42, "Closes #10")
        assert mock_urlopen.call_count == 2


class TestCloseIssue:
    @patch("urllib.request.urlopen")
    def test_closes_issue(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        close_issue("owner/repo", "token", 10)
        mock_urlopen.assert_called_once()


class TestAssignIssue:
    @patch("urllib.request.urlopen")
    def test_assigns_issue_to_user(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        assign_issue("owner/repo", "token", 10, "john.doe")
        mock_urlopen.assert_called_once()

    @patch("urllib.request.urlopen")
    def test_handles_422_error(self, mock_urlopen):
        import urllib.error

        mock_urlopen.side_effect = urllib.error.HTTPError(url="", code=422, msg="", hdrs={}, fp=None)
        assign_issue("owner/repo", "token", 10, "john.doe")


class TestIsDirectPushToMain:
    @patch("subprocess.run")
    def test_returns_true_for_main_branch(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="main\n")
        assert is_direct_push_to_main() is True

    @patch("subprocess.run")
    def test_returns_true_for_master_branch(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="master\n")
        assert is_direct_push_to_main() is True

    @patch("subprocess.run")
    def test_returns_false_for_feature_branch(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="feature/login\n")
        assert is_direct_push_to_main() is False


class TestGetIssueFromMapping:
    def test_returns_issue_number(self):
        mapping = {"stories": {"1-1-user-login": {"issue_number": 10}}}
        assert get_issue_from_mapping(mapping, "1-1-user-login") == 10

    def test_returns_none_when_not_found(self):
        mapping = {"stories": {}}
        assert get_issue_from_mapping(mapping, "1-1-user-login") is None