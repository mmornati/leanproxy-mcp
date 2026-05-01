import os
import sys
from pathlib import Path

import pytest

FIXTURES_DIR = Path(__file__).parent / "fixtures"
STORIES_DIR = FIXTURES_DIR / "stories"
EPICS_DIR = FIXTURES_DIR / "epics"


@pytest.fixture
def fixtures_dir():
    return FIXTURES_DIR


@pytest.fixture
def stories_dir():
    return STORIES_DIR


@pytest.fixture
def epics_dir():
    return EPICS_DIR


@pytest.fixture
def mock_repo():
    return "owner/repo"


@pytest.fixture
def mock_token():
    return "test-token-12345"


@pytest.fixture
def fake_mapping():
    return {
        "last_sync": "2026-05-01T12:00:00Z",
        "epics": {
            "1": {"issue_id": 1001, "issue_number": 10, "title": "Core Infrastructure"},
            "2": {"issue_id": 1002, "issue_number": 20, "title": "Security Features"},
        },
        "stories": {
            "1-1-existing-story": {
                "issue_id": 2001,
                "issue_number": 11,
                "parent_epic": "1",
                "commit_sha": "abc1234",
            }
        },
    }


@pytest.fixture(autouse=True)
def clean_env(monkeypatch):
    monkeypatch.setenv("GITHUB_REPOSITORY", "owner/repo")
    monkeypatch.setenv("GITHUB_TOKEN", "test-token")
    monkeypatch.delenv("SYNC_ALL", raising=False)
    monkeypatch.delenv("EPICS_WORKFLOW_STATUS", raising=False)