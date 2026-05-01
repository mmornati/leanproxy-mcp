import os
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

FIXTURES_DIR = Path(__file__).parent / "fixtures"
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


class TestProcessEpic:
    @patch("bmad_epics_sync.find_issue_by_title")
    @patch("bmad_epics_sync.create_issue")
    @patch("bmad_epics_sync.ensure_label_exists")
    def test_creates_new_epic_when_not_on_github(
        self, mock_ensure, mock_create, mock_find
    ):
        mock_find.return_value = None
        mock_create.return_value = (100, 10)

        from bmad_epics_sync import process_epic

        epic = {"key": "1", "title": "Test Epic", "stories": []}
        mapping = {"epics": {}, "stories": {}}
        content = (EPICS_DIR / "epics-complete.md").read_text()
        result = process_epic("owner/repo", "token", mapping, epic, content)

        assert "1" in result["epics"]
        assert result["epics"]["1"]["issue_number"] == 10
        mock_create.assert_called_once()

    @patch("bmad_epics_sync.find_issue_by_title")
    def test_skips_existing_epic_on_github(
        self, mock_find
    ):
        mock_find.return_value = {"id": "123", "number": 20, "state": "open"}

        from bmad_epics_sync import process_epic

        epic = {"key": "1", "title": "Test Epic", "stories": []}
        mapping = {"epics": {}, "stories": {}}
        content = (EPICS_DIR / "epics-complete.md").read_text()
        result = process_epic("owner/repo", "token", mapping, epic, content)

        assert "1" in result["epics"]
        assert result["epics"]["1"]["issue_number"] == 20

    @patch("bmad_epics_sync.find_issue_by_title")
    @patch("bmad_epics_sync.create_issue")
    @patch("bmad_epics_sync.ensure_label_exists")
    def test_skips_epic_without_step04(
        self, mock_ensure, mock_create, mock_find
    ):
        mock_find.return_value = None

        from bmad_epics_sync import process_epic

        epic = {"key": "1", "title": "Test Epic", "stories": []}
        mapping = {"epics": {}, "stories": {}}
        content = (EPICS_DIR / "epics-incomplete.md").read_text()
        result = process_epic("owner/repo", "token", mapping, epic, content)

        assert "1" not in result["epics"]
        mock_create.assert_not_called()