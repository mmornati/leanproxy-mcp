import os
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

FIXTURES_DIR = Path(__file__).parent / "fixtures"
IMPL_DIR = FIXTURES_DIR / "implementation-artifacts"


class TestIsStoryImplemented:
    def test_detects_story_implemented_marker(self):
        from bmad_sync_lib import is_story_implemented

        content = "## Change Log\n- 2026-05-01: Story 1.1 implemented"
        assert is_story_implemented(content) is True

    def test_detects_story_implemented_case_insensitive(self):
        from bmad_sync_lib import is_story_implemented

        content = "## Change Log\n- 2026-05-01: Story 2.1 IMPLEMENTED"
        assert is_story_implemented(content) is True

    def test_detects_plain_story_implemented(self):
        from bmad_sync_lib import is_story_implemented

        content = "## Change Log\n- 2026-05-01: Story 3.1 implemented — Added feature"
        assert is_story_implemented(content) is True

    def test_no_marker_returns_false(self):
        from bmad_sync_lib import is_story_implemented

        content = "## Change Log\n- 2026-05-01: Story updated"
        assert is_story_implemented(content) is False

    def test_empty_content_returns_false(self):
        from bmad_sync_lib import is_story_implemented

        assert is_story_implemented("") is False


class TestGetCommitAuthor:
    @patch("subprocess.run")
    def test_returns_author_from_git_log(self, mock_run):
        from bmad_sync_lib import get_commit_author

        mock_run.return_value = MagicMock(returncode=0, stdout="John Doe\n")
        result = get_commit_author("test.md")
        assert result == "John Doe"

    @patch.dict(os.environ, {"GITHUB_ACTOR": "test-user"}, clear=False)
    @patch("subprocess.run")
    def test_returns_github_actor_on_error(self, mock_run):
        from bmad_sync_lib import get_commit_author

        mock_run.return_value = MagicMock(returncode=1, stdout="")
        result = get_commit_author("test.md")
        assert result == "test-user"


class TestGetPrForCommit:
    @patch("urllib.request.urlopen")
    def test_returns_pr_info(self, mock_urlopen):
        from bmad_sync_lib import get_pr_for_commit

        mock_response = MagicMock()
        mock_response.read.return_value = b'[{"number": 42, "title": "Feature PR", "body": "Fixes #1"}]'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        result = get_pr_for_commit("owner/repo", "token", "abc123")
        assert result == {"number": 42, "title": "Feature PR", "body": "Fixes #1"}

    @patch("urllib.request.urlopen")
    def test_returns_none_when_no_prs(self, mock_urlopen):
        from bmad_sync_lib import get_pr_for_commit

        mock_response = MagicMock()
        mock_response.read.return_value = b'[]'
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        result = get_pr_for_commit("owner/repo", "token", "abc123")
        assert result is None


class TestLinkPrToIssue:
    @patch("urllib.request.urlopen")
    def test_links_pr_to_issue(self, mock_urlopen):
        from bmad_sync_lib import link_pr_to_issue

        mock_response = MagicMock()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        link_pr_to_issue("owner/repo", "token", 42, 10)
        mock_urlopen.assert_called_once()

    @patch("urllib.request.urlopen")
    def test_handles_422_error(self, mock_urlopen):
        import urllib.error
        from bmad_sync_lib import link_pr_to_issue

        mock_urlopen.side_effect = urllib.error.HTTPError(url="", code=422, msg="", hdrs={}, fp=None)

        link_pr_to_issue("owner/repo", "token", 42, 10)


class TestUpdatePrBody:
    @patch("urllib.request.urlopen")
    def test_updates_pr_body_with_closes_marker(self, mock_urlopen):
        from bmad_sync_lib import update_pr_body

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
        from bmad_sync_lib import close_issue

        mock_response = MagicMock()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        close_issue("owner/repo", "token", 10)
        mock_urlopen.assert_called_once()


class TestAssignIssue:
    @patch("urllib.request.urlopen")
    def test_assigns_issue_to_user(self, mock_urlopen):
        from bmad_sync_lib import assign_issue

        mock_response = MagicMock()
        mock_response.__enter__.return_value = mock_response
        mock_response.__exit__.return_value = None
        mock_urlopen.return_value = mock_response

        assign_issue("owner/repo", "token", 10, "john.doe")
        mock_urlopen.assert_called_once()

    @patch("urllib.request.urlopen")
    def test_handles_422_error(self, mock_urlopen):
        import urllib.error
        from bmad_sync_lib import assign_issue

        mock_urlopen.side_effect = urllib.error.HTTPError(url="", code=422, msg="", hdrs={}, fp=None)

        assign_issue("owner/repo", "token", 10, "john.doe")


class TestIsDirectPushToMain:
    @patch("subprocess.run")
    def test_returns_true_for_main_branch(self, mock_run):
        from bmad_sync_lib import is_direct_push_to_main

        mock_run.return_value = MagicMock(returncode=0, stdout="main\n")
        assert is_direct_push_to_main() is True

    @patch("subprocess.run")
    def test_returns_true_for_master_branch(self, mock_run):
        from bmad_sync_lib import is_direct_push_to_main

        mock_run.return_value = MagicMock(returncode=0, stdout="master\n")
        assert is_direct_push_to_main() is True

    @patch("subprocess.run")
    def test_returns_false_for_feature_branch(self, mock_run):
        from bmad_sync_lib import is_direct_push_to_main

        mock_run.return_value = MagicMock(returncode=0, stdout="feature/login\n")
        assert is_direct_push_to_main() is False


class TestProcessDelivery:
    @patch("bmad_story_delivery.get_commit_author")
    @patch("bmad_story_delivery.is_direct_push_to_main")
    @patch("bmad_story_delivery.get_file_commit_sha")
    @patch("bmad_story_delivery.assign_issue")
    @patch("bmad_story_delivery.close_issue")
    def test_skips_in_progress_story(
        self, mock_close, mock_assign, mock_sha, mock_direct, mock_author
    ):
        from bmad_story_delivery import process_delivery

        content = "# Story 1-1: User Login\n\n## Story\nIn progress..."
        mapping = {"stories": {"1-1-user-login": {"issue_number": 10}}}
        result = process_delivery("owner/repo", "token", mapping, "1-1-user-login.md", content)

        mock_close.assert_not_called()
        mock_assign.assert_not_called()

    @patch("bmad_story_delivery.get_commit_author")
    @patch("bmad_story_delivery.is_direct_push_to_main")
    @patch("bmad_story_delivery.get_file_commit_sha")
    @patch("bmad_story_delivery.assign_issue")
    @patch("bmad_story_delivery.close_issue")
    @patch("bmad_story_delivery.add_comment")
    @patch("bmad_story_delivery.link_pr_to_issue")
    @patch("bmad_story_delivery.update_pr_body")
    def test_delivers_on_pr_branch(
        self, mock_update, mock_link, mock_comment, mock_close, mock_assign, mock_sha, mock_direct, mock_author
    ):
        from bmad_story_delivery import process_delivery

        content = "# Story 1-1: User Login\n\n## Change Log\n- 2026-05-01: Story 1.1 implemented"
        mapping = {"stories": {"1-1-user-login": {"issue_number": 10}}}

        mock_sha.return_value = "abc123"
        mock_author.return_value = "john.doe"
        mock_direct.return_value = False

        result = process_delivery("owner/repo", "token", mapping, "1-1-user-login.md", content, pr_number=42)

        mock_link.assert_called_once_with("owner/repo", "token", 42, 10)
        mock_update.assert_called_once()
        mock_assign.assert_called_once_with("owner/repo", "token", 10, "john.doe")
        mock_close.assert_not_called()

    @patch("bmad_story_delivery.get_commit_author")
    @patch("bmad_story_delivery.is_direct_push_to_main")
    @patch("bmad_story_delivery.get_file_commit_sha")
    @patch("bmad_story_delivery.assign_issue")
    @patch("bmad_story_delivery.close_issue")
    @patch("bmad_story_delivery.add_comment")
    @patch("bmad_story_delivery.link_pr_to_issue")
    @patch("bmad_story_delivery.update_pr_body")
    def test_delivers_on_direct_push_to_main(
        self, mock_update, mock_link, mock_comment, mock_close, mock_assign, mock_sha, mock_direct, mock_author
    ):
        from bmad_story_delivery import process_delivery

        content = "# Story 1-1: User Login\n\n## Change Log\n- 2026-05-01: Story 1.1 implemented"
        mapping = {"stories": {"1-1-user-login": {"issue_number": 10}}}

        mock_sha.return_value = "abc123"
        mock_author.return_value = "john.doe"
        mock_direct.return_value = True

        result = process_delivery("owner/repo", "token", mapping, "1-1-user-login.md", content)

        mock_assign.assert_called_once_with("owner/repo", "token", 10, "john.doe")
        mock_close.assert_called_once_with("owner/repo", "token", 10)
        mock_link.assert_not_called()

    @patch("bmad_story_delivery.get_commit_author")
    @patch("bmad_story_delivery.is_direct_push_to_main")
    @patch("bmad_story_delivery.get_file_commit_sha")
    def test_warns_when_no_issue_in_mapping(
        self, mock_sha, mock_direct, mock_author
    ):
        from bmad_story_delivery import process_delivery

        content = "# Story 1-1: User Login\n\n## Change Log\n- 2026-05-01: Story 1.1 implemented"
        mapping = {"stories": {}}

        mock_sha.return_value = "abc123"
        mock_author.return_value = "john.doe"
        mock_direct.return_value = True

        result = process_delivery("owner/repo", "token", mapping, "1-1-user-login.md", content)

    @patch("bmad_story_delivery.get_commit_author")
    @patch("bmad_story_delivery.is_direct_push_to_main")
    @patch("bmad_story_delivery.get_file_commit_sha")
    @patch("bmad_story_delivery.assign_issue")
    @patch("bmad_story_delivery.close_issue")
    def test_warns_when_no_pr_and_not_direct_push(
        self, mock_close, mock_assign, mock_sha, mock_direct, mock_author
    ):
        from bmad_story_delivery import process_delivery

        content = "# Story 1-1: User Login\n\n## Change Log\n- 2026-05-01: Story 1.1 implemented"
        mapping = {"stories": {"1-1-user-login": {"issue_number": 10}}}

        mock_sha.return_value = "abc123"
        mock_author.return_value = "john.doe"
        mock_direct.return_value = False

        result = process_delivery("owner/repo", "token", mapping, "1-1-user-login.md", content)

        mock_assign.assert_not_called()
        mock_close.assert_not_called()


class TestGetIssueFromMapping:
    def test_returns_issue_number(self):
        from bmad_sync_lib import get_issue_from_mapping

        mapping = {"stories": {"1-1-user-login": {"issue_number": 10}}}
        assert get_issue_from_mapping(mapping, "1-1-user-login") == 10

    def test_returns_none_when_not_found(self):
        from bmad_sync_lib import get_issue_from_mapping

        mapping = {"stories": {}}
        assert get_issue_from_mapping(mapping, "1-1-user-login") is None