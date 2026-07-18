# Acceptance Auditor — Spec Compliance Review
## Target: pkg/bouncer/injection/ — Story 13-1 vs Story spec

Story spec: /Users/mmornati/Projects/leanproxy-mcp/_bmad-output/implementation-artifacts/13-1-injection-classifier.md

Key ACs:
- "ignore previous instructions" / "you are now..." → risk_score 0-100 based on weighted matches; preserve original payload
- No matches → risk_score=0, no overhead (NFR11)
- Custom pattern in leanproxy.yaml → load on startup, individual enable/disable
- >=95% recall on 200-payload corpus (FR43 AC)
- Static binary <20MB; backward compatibility; camelCase Go

Check:
1. Is the original payload preserved in Result?
2. Is risk_score 0 when no matches?
3. Are custom patterns loadable from config and individually enablable/disablable?
4. Is threshold configurable?
5. Is there any integration point with leanproxy.yaml? (Story says "custom pattern in leanproxy.yaml → load on startup")
6. Benchmark demonstrates <1ms p95 overhead?
7. Are there 14 default patterns as specified?
