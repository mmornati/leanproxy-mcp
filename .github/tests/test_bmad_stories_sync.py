import json
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


class TestStoryProcessNew:
    @patch("bmad_stories_sync.create_issue")
    @patch("bmad_stories_sync.ensure_label_exists")
    @patch("bmad_stories_sync.link_sub_issue")
    @patch("bmad_stories_sync.load_mapping")
    @patch("bmad_stories_sync.save_mapping")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_creates_story_with_frontmatter(
        self, mock_sha, mock_save, mock_load, mock_link, mock_ensure, mock_create
    ):
        mock_sha.return_value = "abc123"
        mock_load.return_value = {
            "last_sync": None,
            "epics": {"1": {"issue_number": 10}},
            "stories": {},
        }
        mock_create.return_value = (200, 15)

        from bmad_stories_sync import process_story_new

        file_path = str(STORIES_DIR / "1-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = process_story_new("owner/repo", "token", mock_load.return_value, file_path, content)

        assert "1-1-test-story" in mapping["stories"]
        assert mapping["stories"]["1-1-test-story"]["parent_epic"] == "1"

    @patch("bmad_stories_sync.create_issue")
    @patch("bmad_stories_sync.ensure_label_exists")
    @patch("bmad_stories_sync.link_sub_issue")
    @patch("bmad_stories_sync.load_mapping")
    @patch("bmad_stories_sync.save_mapping")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_creates_story_without_frontmatter(
        self, mock_sha, mock_save, mock_load, mock_link, mock_ensure, mock_create
    ):
        mock_sha.return_value = "def456"
        mock_load.return_value = {
            "last_sync": None,
            "epics": {"3": {"issue_number": 30}},
            "stories": {},
        }
        mock_create.return_value = (300, 35)

        from bmad_stories_sync import process_story_new

        file_path = str(STORIES_DIR / "3-1-no-frontmatter.md")
        content = Path(file_path).read_text()
        mapping = process_story_new("owner/repo", "token", mock_load.return_value, file_path, content)

        assert "3-1-no-frontmatter" in mapping["stories"]
        assert mapping["stories"]["3-1-no-frontmatter"]["parent_epic"] == "3"

    @patch("bmad_stories_sync.create_issue")
    @patch("bmad_stories_sync.ensure_label_exists")
    @patch("bmad_stories_sync.load_mapping")
    @patch("bmad_stories_sync.save_mapping")
    @patch("bmad_stories_sync.get_file_commit_sha")
    def test_creates_story_when_epic_missing(
        self, mock_sha, mock_save, mock_load, mock_ensure, mock_create
    ):
        mock_sha.return_value = "ghi789"
        mock_load.return_value = {
            "last_sync": None,
            "epics": {},
            "stories": {},
        }
        mock_create.return_value = (400, 40)

        from bmad_stories_sync import process_story_new

        file_path = str(STORIES_DIR / "2-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = process_story_new("owner/repo", "token", mock_load.return_value, file_path, content)

        assert "2-1-test-story" in mapping["stories"]
        assert mapping["stories"]["2-1-test-story"]["parent_epic"] == "2"
        mock_create.assert_called()


class TestStoryProcessModified:
    @patch("bmad_stories_sync.add_comment")
    @patch("bmad_stories_sync.get_file_commit_sha")
    @patch("bmad_stories_sync.get_file_commit_url")
    @patch("bmad_stories_sync.load_mapping")
    @patch("bmad_stories_sync.save_mapping")
    def test_adds_modification_comment(
        self, mock_save, mock_load, mock_url, mock_sha, mock_add
    ):
        mock_sha.return_value = "jkl012"
        mock_url.return_value = "https://github.com/owner/repo/commit/jkl012"
        mock_load.return_value = {
            "last_sync": None,
            "epics": {},
            "stories": {"1-1-test-story": {"issue_number": 15}},
        }

        from bmad_stories_sync import process_story_modified

        file_path = str(STORIES_DIR / "1-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = process_story_modified(
            "owner/repo", "token", mock_load.return_value, file_path, content
        )

        assert mock_add.called
        call_args = mock_add.call_args
        comment_body = call_args[0][3]
        assert "jkl012" in comment_body
        assert "owner/repo" in comment_body

    @patch("bmad_stories_sync.add_comment")
    @patch("bmad_stories_sync.get_file_commit_sha")
    @patch("bmad_stories_sync.load_mapping")
    @patch("bmad_stories_sync.save_mapping")
    def test_updates_commit_sha(self, mock_save, mock_load, mock_sha, mock_add):
        mock_sha.return_value = "new123"
        mock_load.return_value = {
            "last_sync": None,
            "epics": {},
            "stories": {"1-1-test-story": {"issue_number": 15, "commit_sha": "old456"}},
        }

        from bmad_stories_sync import process_story_modified

        file_path = str(STORIES_DIR / "1-1-test-story.md")
        content = Path(file_path).read_text()
        mapping = process_story_modified(
            "owner/repo", "token", mock_load.return_value, file_path, content
        )

        assert mapping["stories"]["1-1-test-story"]["commit_sha"] == "new123"


class TestStoryIssueBody:
    def test_body_contains_full_content(self):
        from bmad_sync_lib import build_issue_body

        content = (STORIES_DIR / "1-1-test-story.md").read_text()
        body = build_issue_body(content)

        assert "Story 1-1" in body
        assert "User Story" in body
        assert "Initialize Test Project" in body


class TestGetStoryFiles:
    def test_filters_implementation_artifacts(self):
        from bmad_stories_sync import get_story_files

        new_files = [
            "_bmad-output/planning-artifacts/prd.md",
            "_bmad-output/implementation-artifacts/1-1-test-story.md",
            "_bmad-output/implementation-artifacts/2-1-test-story.md",
        ]
        modified_files = [
            "_bmad-output/planning-artifacts/epics.md",
            "_bmad-output/implementation-artifacts/3-1-no-frontmatter.md",
        ]

        new_stories, mod_stories = get_story_files(new_files, modified_files)

        assert len(new_stories) == 2
        assert "1-1-test-story.md" in new_stories[0]
        assert "2-1-test-story.md" in new_stories[1]
        assert len(mod_stories) == 1
        assert "3-1-no-frontmatter.md" in mod_stories[0]