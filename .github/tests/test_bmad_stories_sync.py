import os
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

FIXTURES_DIR = Path(__file__).parent / "fixtures"
STORIES_DIR = FIXTURES_DIR / "stories"


class TestStoryTitleExtraction:
    def test_extracts_title_with_frontmatter(self):
        from bmad_sync_lib import parse_story_title

        content = (STORIES_DIR / "1-1-test-story.md").read_text()
        title = parse_story_title(content)
        assert title == "Initialize Test Project"

    def test_extracts_title_without_frontmatter(self):
        from bmad_sync_lib import parse_story_title

        content = (STORIES_DIR / "3-1-no-frontmatter.md").read_text()
        title = parse_story_title(content)
        assert title == "Story Without Frontmatter"


class TestStoryEpicLinking:
    def test_epic_from_frontmatter(self):
        from bmad_sync_lib import extract_epic_key

        content = (STORIES_DIR / "2-1-test-story.md").read_text()
        epic = extract_epic_key(content, "2-1-test-story.md")
        assert epic == "2"

    def test_epic_fallback_to_filename(self):
        from bmad_sync_lib import extract_epic_key

        content = (STORIES_DIR / "3-1-no-frontmatter.md").read_text()
        epic = extract_epic_key(content, "3-1-no-frontmatter.md")
        assert epic == "3"


class TestProcessStory:
    @patch("bmad_stories_sync.find_issue_by_title")
    @patch("bmad_stories_sync.create_issue")
    @patch("bmad_stories_sync.ensure_label_exists")
    @patch("bmad_stories_sync.link_sub_issue")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_creates_new_story_when_not_on_github(
        self, mock_sha, mock_link, mock_ensure, mock_create, mock_find
    ):
        mock_sha.return_value = "abc123"
        mock_find.return_value = None
        mock_create.return_value = (200, 15)

        from bmad_stories_sync import process_story

        file_path = str(STORIES_DIR / "1-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = {"epics": {"1": {"issue_number": 10}}, "stories": {}}
        result = process_story("owner/repo", "token", mapping, file_path, content)

        assert "1-1-test-story" in result["stories"]
        assert result["stories"]["1-1-test-story"]["issue_number"] == 15
        mock_create.assert_called_once()

    @patch("bmad_stories_sync.find_issue_by_title")
    @patch("bmad_stories_sync.add_comment")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_updates_existing_story_on_github(
        self, mock_sha, mock_add, mock_find
    ):
        mock_sha.return_value = "def456"
        mock_find.return_value = {"id": "123", "number": 20, "state": "open"}

        from bmad_stories_sync import process_story

        file_path = str(STORIES_DIR / "1-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = {"epics": {}, "stories": {}}
        result = process_story("owner/repo", "token", mapping, file_path, content)

        assert "1-1-test-story" in result["stories"]
        assert result["stories"]["1-1-test-story"]["issue_number"] == 20
        mock_add.assert_called_once()

    @patch("bmad_stories_sync.find_issue_by_title")
    @patch("bmad_stories_sync.create_issue")
    @patch("bmad_stories_sync.ensure_label_exists")
    @patch("bmad_stories_sync.link_sub_issue")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_creates_story_without_frontmatter(
        self, mock_sha, mock_link, mock_ensure, mock_create, mock_find
    ):
        mock_sha.return_value = "ghi789"
        mock_find.return_value = None
        mock_create.return_value = (300, 35)

        from bmad_stories_sync import process_story

        file_path = str(STORIES_DIR / "3-1-no-frontmatter.md")
        content = Path(file_path).read_text()
        mapping = {"epics": {"3": {"issue_number": 30}}, "stories": {}}
        result = process_story("owner/repo", "token", mapping, file_path, content)

        assert "3-1-no-frontmatter" in result["stories"]
        assert result["stories"]["3-1-no-frontmatter"]["parent_epic"] == "3"

    @patch("bmad_stories_sync.find_issue_by_title")
    @patch("bmad_stories_sync.create_issue")
    @patch("bmad_stories_sync.ensure_label_exists")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_creates_story_when_epic_missing(
        self, mock_sha, mock_ensure, mock_create, mock_find
    ):
        mock_sha.return_value = "jkl012"
        mock_find.return_value = None
        mock_create.return_value = (400, 40)

        from bmad_stories_sync import process_story

        file_path = str(STORIES_DIR / "2-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = {"epics": {}, "stories": {}}
        result = process_story("owner/repo", "token", mapping, file_path, content)

        assert "2-1-test-story" in result["stories"]
        assert result["stories"]["2-1-test-story"]["parent_epic"] == "2"


class TestStoryIssueBody:
    def test_body_contains_full_content(self):
        from bmad_sync_lib import build_issue_body

        content = (STORIES_DIR / "1-1-test-story.md").read_text()
        body = build_issue_body(content)

        assert "Story 1-1" in body
        assert "User Story" in body
        assert "Initialize Test Project" in body