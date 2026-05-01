# Story 3-1: Story Without Frontmatter

## Story Header

| Field | Value |
|-------|-------|
| **ID** | 3-1 |
| **Title** | Story Without Frontmatter |

## Story Requirements

### User Story

As a developer,
I want to test fallback epic detection,
so that stories without frontmatter still link correctly.

## Developer Context

### Technical Requirements

- Epic key should be extracted from filename prefix
- Filename 3-1-no-frontmatter → Epic 3

## Implementation Checklist

- [ ] Verify filename parsing works