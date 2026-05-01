import json
import os
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

FIXTURES_DIR = Path(__file__).parent / "fixtures"
STORIES_DIR = FIXTURES_DIR / "stories"
EPICS_DIR = FIXTURES_DIR / "epics"


class TestEpicParsing:
    def test_parses_complete_epics_file(self):
        from bmad_epics_sync import parse_epics_md

        content = (EPICS_DIR / "epics-complete.md").read_text()
        epics = parse_epics_md(content)

        assert len(epics) == 3
        assert epics[0]["key"] == "1"
        assert epics[0]["title"] == "Core Infrastructure"
        assert len(epics[0]["stories"]) == 2
        assert epics[1]["key"] == "2"
        assert epics[2]["key"] == "3"

    def test_skips_incomplete_epics(self):
        from bmad_sync_lib import is_step_04_completed

        content = (EPICS_DIR / "epics-incomplete.md").read_text()
        assert is_step_04_completed(content) is False

        content = (EPICS_DIR / "epics-complete.md").read_text()
        assert is_step_04_completed(content) is True


class TestEpicProcessNew:
    @patch("bmad_sync_lib.get_file_commit_sha")
    @patch("bmad_epics_sync.save_mapping")
    @patch("bmad_epics_sync.load_mapping")
    @patch("bmad_epics_sync.link_sub_issue")
    @patch("bmad_epics_sync.ensure_label_exists")
    @patch("bmad_epics_sync.create_issue")
    def test_creates_epic_issues(
        self, mock_create, mock_ensure, mock_link, mock_load, mock_save, mock_sha
    ):
        mock_sha.return_value = "abc123"
        mock_load.return_value = {"last_sync": None, "epics": {}, "stories": {}}
        mock_create.side_effect = [(100, 10), (200, 11), (201, 12), (300, 13), (301, 14), (302, 15), (303, 16), (304, 17)]

        from bmad_epics_sync import process_epics_new

        content = (EPICS_DIR / "epics-complete.md").read_text()
        mapping = process_epics_new("owner/repo", "token", {"epics": {}, "stories": {}}, content)

        assert "1" in mapping["epics"]
        assert "2" in mapping["epics"]
        assert "3" in mapping["epics"]
        assert mock_create.call_count >= 8  # 3 epics + 5 stories

    @patch("bmad_epics_sync.ensure_label_exists")
    @patch("bmad_epics_sync.create_issue")
    @patch("bmad_epics_sync.load_mapping")
    @patch("bmad_epics_sync.save_mapping")
    def test_skips_incomplete_epics(self, mock_save, mock_load, mock_create, mock_ensure):
        mock_load.return_value = {"last_sync": None, "epics": {}, "stories": {}}

        from bmad_epics_sync import process_epics_new

        content = (EPICS_DIR / "epics-incomplete.md").read_text()
        result = process_epics_new("owner/repo", "token", {"epics": {}, "stories": {}}, content)

        assert result["epics"] == {}
        mock_create.assert_not_called()


class TestEpicProcessModified:
    @patch("bmad_sync_lib.add_comment")
    @patch("bmad_sync_lib.get_file_commit_sha")
    @patch("bmad_epics_sync.load_mapping")
    @patch("bmad_epics_sync.save_mapping")
    def test_adds_comment_on_modification(
        self, mock_save, mock_load, mock_sha, mock_add_comment
    ):
        mock_sha.return_value = "def456"
        mock_load.return_value = {
            "last_sync": None,
            "epics": {"1": {"issue_number": 10}},
            "stories": {},
        }

        from bmad_epics_sync import process_epics_modified

        content = (EPICS_DIR / "epics-complete.md").read_text()
        mapping = process_epics_modified("owner/repo", "token", mock_load.return_value, content)

        assert mock_add_comment.called


class TestEpicLinking:
    @patch("bmad_epics_sync.save_mapping")
    @patch("bmad_epics_sync.link_sub_issue")
    @patch("bmad_epics_sync.ensure_label_exists")
    @patch("bmad_epics_sync.create_issue")
    def test_links_stories_as_sub_issues(
        self, mock_create, mock_ensure, mock_link, mock_save
    ):
        mock_create.side_effect = [(100, 10), (200, 11), (201, 12), (300, 13), (301, 14), (302, 15), (303, 16), (304, 17)]

        from bmad_epics_sync import process_epics_new

        content = (EPICS_DIR / "epics-complete.md").read_text()
        mapping = {"epics": {}, "stories": {}}
        process_epics_new("owner/repo", "token", mapping, content)

        assert mock_link.call_count >= 5  # 5 total stories linked to their epics
        assert "epics" in mapping