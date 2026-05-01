# BMad GitHub Sync Workflow

Automatically syncs BMad planning artifacts to GitHub Issues and Epics.

## Structure

```
.github/
├── scripts/
│   └── bmad_sync.py          # Main sync script
├── tests/
│   ├── requirements.txt      # Python dependencies
│   └── test_bmad_sync.py     # Unit tests (23 tests)
├── fixtures/
│   ├── epics-test.md         # Mock epics for testing
│   └── implementation-artifacts/
│       └── *.md              # Mock stories for testing
└── workflows/
    └── bmad-sync.yml        # GitHub Actions workflow
```

## How It Works

| File Changed | Action |
|--------------|--------|
| `planning-artifacts/epics.md` (new) | Creates Epic issue + Story issues as sub-issues |
| `planning-artifacts/epics.md` (modified) | Comments on existing Epic issue |
| `implementation-artifacts/{story}.md` (new) | Creates Story issue, links to Epic as sub-issue |
| `implementation-artifacts/{story}.md` (modified) | Comments on existing Story issue |

### Issue Tracking

Mappings are stored in `_bmad-output/.issue-mapping.json`:

```json
{
  "last_sync": "2026-05-01T12:00:00Z",
  "epics": {
    "1": { "issue_id": 123, "issue_number": 123, "title": "Epic 1" }
  },
  "stories": {
    "1-1-user-auth": { "issue_id": 124, "issue_number": 124, "parent_epic": "1" }
  }
}
```

### Labels

New issues are automatically labeled with `bmad` (created if not exists).

## Quickstart

### 1. Add GitHub Token Secret

1. Go to GitHub repo → Settings → Secrets → Actions
2. Add `BMAD_GITHUB_TOKEN` with your Personal Access Token

### 2. Run Locally

```bash
# Create virtual environment (if not exists)
python3 -m venv .venv

# Activate virtual environment
source .venv/bin/activate  # Linux/macOS
# or: .\.venv\Scripts\Activate.ps1  # Windows

# Install dependencies
pip install -r .github/tests/requirements.txt

# Run tests
pytest .github/tests/test_bmad_sync.py -v

# Run sync script (requires GITHUB_REPOSITORY and GITHUB_TOKEN env vars)
GITHUB_REPOSITORY=owner/repo GITHUB_TOKEN=your_token python .github/scripts/bmad_sync.py
```

### 3. Test the Workflow

Push a markdown file to `_bmad-output/` to trigger the workflow:

```bash
git add _bmad-output/planning-artifacts/epics.md
git commit -m "test: trigger bmad sync workflow"
git push
```

## BMad File Conventions

### Epics File (`planning-artifacts/epics.md`)

```markdown
# Epic 1: User Authentication

## Stories

### 1.1 User Login
Story description...
```

### Story Files (`implementation-artifacts/{epic}-{story}-{name}.md`)

```markdown
# Story 1-1: User Login
...
```

## Workflow Configuration

- **Trigger**: Push to `_bmad-output/**/*.md`
- **Python**: 3.11
- **Runs**: `sync` job (creates issues) + `test` job (runs tests + actionlint)

## Extending

To add new file types or behaviors, modify `.github/scripts/bmad_sync.py`:

- `process_epics_new()` / `process_epics_modified()` - Handle epics.md
- `process_story_new()` / `process_story_modified()` - Handle story files
- `create_issue()` / `add_comment()` / `link_sub_issue()` - GitHub API calls