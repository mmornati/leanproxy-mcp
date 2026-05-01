import json
import os
import sys
import tempfile
import urllib.error
from pathlib import Path
from unittest.mock import patch, MagicMock

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))
import bmad_sync


class TestSlugify:
    def test_basic_slug(self):
        assert bmad_sync.slugify("User Login") == "user-login"

    def test_special_characters(self):
        assert bmad_sync.slugify("User Login (Test)") == "user-login-test"

    def test_multiple_spaces(self):
        assert bmad_sync.slugify("User  Login") == "user-login"

    def test_alphanumeric(self):
        assert bmad_sync.slugify("API Key v2") == "api-key-v2"


class TestParseEpicsMd:
    def test_single_epic_single_story(self):
        content = """# Epic 1: Authentication

## Stories

### 1.1 User Login
Story content here.
"""
        epics = bmad_sync.parse_epics_md(content)
        assert len(epics) == 1
        assert epics[0]["key"] == "1"
        assert epics[0]["title"] == "Authentication"
        assert len(epics[0]["stories"]) == 1
        assert epics[0]["stories"][0]["title"] == "User Login"

    def test_multiple_epics(self):
        content = """# Epic 1: Authentication

## Stories

### 1.1 User Login

---

# Epic 2: API Management

## Stories

### 2.1 API Key Generation
"""
        epics = bmad_sync.parse_epics_md(content)
        assert len(epics) == 2
        assert epics[0]["key"] == "1"
        assert epics[1]["key"] == "2"

    def test_epic_with_multiple_stories(self):
        content = """# Epic 1: Authentication

## Stories

### 1.1 User Login

### 1.2 User Registration

### 1.3 Password Reset
"""
        epics = bmad_sync.parse_epics_md(content)
        assert len(epics[0]["stories"]) == 3
        assert epics[0]["stories"][0]["key"] == "1-1-user-login"
        assert epics[0]["stories"][1]["key"] == "1-2-user-registration"
        assert epics[0]["stories"][2]["key"] == "1-3-password-reset"

    def test_empty_content(self):
        epics = bmad_sync.parse_epics_md("")
        assert epics == []


class TestLoadMapping:
    def test_load_existing_mapping(self, tmp_path):
        mapping_file = tmp_path / ".issue-mapping.json"
        test_data = {"last_sync": "2026-05-01T12:00:00Z", "epics": {"1": {"issue_id": 123}}, "stories": {}}
        mapping_file.write_text(json.dumps(test_data))

        with patch.object(bmad_sync, "MAPPING_FILE", mapping_file):
            result = bmad_sync.load_mapping()

        assert result["last_sync"] == "2026-05-01T12:00:00Z"
        assert result["epics"]["1"]["issue_id"] == 123

    def test_load_nonexistent_mapping(self, tmp_path):
        mapping_file = tmp_path / ".issue-mapping.json"

        with patch.object(bmad_sync, "MAPPING_FILE", mapping_file):
            result = bmad_sync.load_mapping()

        assert result["last_sync"] is None
        assert result["epics"] == {}
        assert result["stories"] == {}


class TestSaveMapping:
    def test_save_mapping(self, tmp_path):
        mapping_file = tmp_path / ".issue-mapping.json"
        test_data = {"last_sync": None, "epics": {}, "stories": {}}

        with patch.object(bmad_sync, "MAPPING_FILE", mapping_file):
            bmad_sync.save_mapping(test_data)

        saved = json.loads(mapping_file.read_text())
        assert saved["last_sync"] is not None
        assert "last_sync" in saved


class TestEnsureLabelExists:
    @patch("urllib.request.urlopen")
    def test_label_exists(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'{}'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        bmad_sync.ensure_label_exists("owner/repo", "token")
        mock_urlopen.assert_called_once()

    @patch("urllib.request.urlopen")
    @patch("urllib.request.Request")
    def test_label_not_exists_creates(self, mock_request, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'{}'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.side_effect = [
            urllib.error.HTTPError(None, 404, None, None, None),
            mock_response,
        ]

        bmad_sync.ensure_label_exists("owner/repo", "token")

        assert mock_urlopen.call_count == 2


class TestCreateIssue:
    @patch("urllib.request.urlopen")
    def test_create_issue(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = json.dumps({"id": 123, "number": 45}).encode()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        issue_id, issue_number = bmad_sync.create_issue("owner/repo", "token", "Test Issue", "Body", ["bmad"])

        assert issue_id == 123
        assert issue_number == 45
        mock_urlopen.assert_called_once()


class TestAddComment:
    @patch("urllib.request.urlopen")
    def test_add_comment(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'{}'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        bmad_sync.add_comment("owner/repo", "token", 45, "Test comment")

        mock_urlopen.assert_called_once()


class TestLinkSubIssue:
    @patch("urllib.request.urlopen")
    def test_link_sub_issue(self, mock_urlopen):
        mock_response = MagicMock()
        mock_response.read.return_value = b'{}'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        bmad_sync.link_sub_issue("owner/repo", "token", 45, 123)

        mock_urlopen.assert_called_once()

    @patch("urllib.request.urlopen")
    def test_link_sub_issue_422_skipped(self, mock_urlopen):
        mock_urlopen.side_effect = urllib.error.HTTPError(None, 422, None, None, None)

        bmad_sync.link_sub_issue("owner/repo", "token", 45, 123)


class TestProcessEpicsNew:
    @patch.object(bmad_sync, "ensure_label_exists")
    @patch.object(bmad_sync, "create_issue")
    @patch.object(bmad_sync, "link_sub_issue")
    def test_process_new_epics(self, mock_link, mock_create, mock_ensure):
        mock_create.side_effect = [
            (111, 11),
            (222, 22),
            (333, 33),
        ]

        content = """# Epic 1: Auth

## Stories

### 1.1 User Login
"""
        mapping = {"epics": {}, "stories": {}}

        result = bmad_sync.process_epics_new("owner/repo", "token", mapping, content)

        assert "1" in result["epics"]
        assert result["epics"]["1"]["issue_id"] == 111
        assert "1-1-user-login" in result["stories"]
        assert result["stories"]["1-1-user-login"]["issue_id"] == 222


class TestProcessEpicsModified:
    @patch.object(bmad_sync, "add_comment")
    def test_process_modified_epics(self, mock_comment):
        content = """# Epic 1: Auth Updated

## Stories

### 1.1 User Login Updated
"""
        mapping = {
            "epics": {
                "1": {"issue_id": 111, "issue_number": 11, "title": "Auth"},
            },
            "stories": {},
        }

        bmad_sync.process_epics_modified("owner/repo", "token", mapping, content)

        mock_comment.assert_called_once()


class TestProcessStoryNew:
    @patch.object(bmad_sync, "create_issue")
    @patch.object(bmad_sync, "link_sub_issue")
    def test_process_new_story(self, mock_link, mock_create):
        mock_create.return_value = (456, 56)

        mapping = {
            "epics": {
                "1": {"issue_id": 111, "issue_number": 11},
            },
            "stories": {},
        }

        result = bmad_sync.process_story_new(
            "owner/repo", "token", mapping,
            "_bmad-output/implementation-artifacts/1-2-test.md",
            "# Story content"
        )

        assert "1-2-test" in result["stories"]
        assert result["stories"]["1-2-test"]["issue_id"] == 456
        mock_link.assert_called_once_with("owner/repo", "token", 11, 456)

    @patch.object(bmad_sync, "create_issue")
    def test_process_new_story_no_epic(self, mock_create):
        mapping = {
            "epics": {},
            "stories": {},
        }

        result = bmad_sync.process_story_new(
            "owner/repo", "token", mapping,
            "_bmad-output/implementation-artifacts/1-2-test.md",
            "# Story content"
        )

        mock_create.assert_not_called()
        assert "1-2-test" not in result["stories"]


class TestProcessStoryModified:
    @patch.object(bmad_sync, "add_comment")
    def test_process_modified_story(self, mock_comment):
        mapping = {
            "stories": {
                "1-1-test": {"issue_id": 222, "issue_number": 22},
            },
        }

        bmad_sync.process_story_modified(
            "owner/repo", "token", mapping,
            "_bmad-output/implementation-artifacts/1-1-test.md",
            "# Updated story"
        )

        mock_comment.assert_called_once()


class TestGetChangedFiles:
    @patch("subprocess.run")
    def test_get_changed_files(self, mock_run):
        def create_mock_result(stdout, returncode):
            mock = MagicMock()
            mock.stdout = stdout
            mock.returncode = returncode
            return mock

        mock_run.side_effect = [
            create_mock_result("_bmad-output/file1.md\n_bmad-output/file2.md\n", 0),
            create_mock_result("", 0),
            create_mock_result("", 0),
            create_mock_result("", 1),
        ]

        new_files, modified_files = bmad_sync.get_changed_files()

        assert "_bmad-output/file2.md" in new_files
        assert "_bmad-output/file1.md" in modified_files


if __name__ == "__main__":
    pytest.main([__file__, "-v"])