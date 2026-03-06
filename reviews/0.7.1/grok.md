# Code Review Report: Kula (https://github.com/c0m4r/kula)

## Overall Summary

Kula is a lightweight, self-contained Linux server monitoring tool implemented primarily in Go, with a web UI using embedded JavaScript/CSS/HTML and a terminal UI using Bubble Tea. It collects metrics from `/proc` and `/sys`, stores them in a tiered ring-buffer storage system, and provides real-time dashboards via WebSocket and REST API. The project emphasizes minimal dependencies, performance, and security features like Landlock sandboxing and Argon2 authentication.

The code is well-structured, modular, and follows Go idioms, with good error handling and concurrency management. Performance is a strength, with efficient metric collection and storage designed for low overhead. Security is addressed proactively, but some areas (e.g., input validation, potential DoS vectors) could be improved. The codebase is small and maintainable, suitable for its scope as a single-binary tool.

Key strengths:
- Zero external runtime dependencies (beyond stdlib and embedded assets).
- Efficient ring-buffer storage with aggregation for long-term retention.
- Security features like sandboxing and secure auth.

Areas for improvement:
- Incomplete test coverage (no unit tests visible in core files).
- Some redundancy in metric parsing logic.
- Potential for better documentation and comments in complex areas like storage.

**Recommendation:** The project is production-ready for small-scale use but would benefit from automated tests, more robust error recovery, and expanded documentation.

## Scoring

- **Code Quality:** 8/10 – Clean, idiomatic Go; modular design; but lacks tests and some comments.
- **Performance:** 9/10 – Low-overhead collection; efficient storage; minor optimizations possible in I/O.
- **Security:** 7/10 – Good use of sandboxing and hashing; but potential issues in auth rate limiting and input handling.

## Detailed Findings

### Code Quality

#### High Severity
- **Lack of Unit/Integration Tests (High):** No test files or assertions found in core packages (e.g., collector, storage, web). This risks regressions during changes.
  - **Recommendation:** Add Go tests for key functions (e.g., metric parsing in collector, aggregation in storage). Use table-driven tests for parsers. Target 70%+ coverage.

#### Medium Severity
- **Redundant Parsing Logic (Medium):** Metric collection (e.g., in collector/cpu.go, network.go) repeats similar file reading/parsing patterns without a shared utility.
  - **Recommendation:** Extract a common `parseFileLines` or `readProcFile` helper to reduce duplication and improve maintainability.
- **Magic Numbers and Constants (Medium):** Hardcoded values (e.g., 384 in countLoggedInUsers, 1024*1024 in gen-mock-data) lack explanations.
  - **Recommendation:** Define named constants (e.g., `const UtmpRecordSize = 384`) with comments explaining origins.
- **Incomplete TUI Review (Medium):** TUI code (internal/tui/) not fetched; assume similar quality, but verify for Bubble Tea best practices.
  - **Recommendation:** Ensure TUI handles terminal resizing and errors gracefully; add tests if possible.

#### Low Severity
- **Documentation Gaps (Low):** Some functions lack godoc comments (e.g., collectTCPStats); README is good but code-level docs sparse.
  - **Recommendation:** Add godoc to public funcs; use tools like godoc or goreportcard.
- **Python Script Quality (addons/inspect_tier.py) (Low):** Basic script; uses outdated struct.unpack in places; no tests.
  - **Recommendation:** Modernize with struct.Struct for unpacking; add type hints and tests.

### Performance

#### Medium Severity
- **Frequent Header Writes in Storage (Medium):** In storage/tier.go, headers are written every 10 samples, which could cause I/O spikes on high-frequency tiers.
  - **Recommendation:** Make header updates asynchronous or less frequent (e.g., every 60s via timer); use fsync sparingly.
- **Full Scans in QueryLatest Fallback (Medium):** Though cached, initial or empty-store QueryLatest scans the entire tier file.
  - **Recommendation:** Store latest sample offset in header for O(1) reads.

#### Low Severity
- **Buffered I/O Opportunities (Low):** Some file reads (e.g., /proc/net/snmp) use os.ReadFile; others use bufio.Scanner inconsistently.
  - **Recommendation:** Standardize on bufio for all proc/sys reads to reduce syscalls.
- **JSON Encoding Overhead (Low):** Samples are JSON-marshaled for storage; for high throughput, consider binary (e.g., CBOR).
  - **Recommendation:** Profile with pprof; switch if marshaling dominates CPU.
- **WebSocket Ping/Pong (Low):** Fixed 50s pings; could adapt based on activity.
  - **Recommendation:** Use adaptive intervals; monitor with metrics.

### Security

#### High Severity
- **Rate Limiting Bypass Potential (High):** Auth rate limiter uses client IP, but lacks proxy header validation (e.g., X-Forwarded-For could be spoofed).
  - **Recommendation:** Validate trusted proxies; use a more robust limiter like golang.org/x/time/rate.

#### Medium Severity
- **Session Token Exposure (Medium):** Tokens in cookies and Authorization headers; no CSRF protection for API.
  - **Recommendation:** Add CSRF tokens for state-changing endpoints (though minimal here); use secure, HttpOnly cookies.
- **Input Validation Gaps (Medium):** API queries (e.g., /api/history) parse times without bounds checks beyond 31 days; potential for large queries exhausting resources.
  - **Recommendation:** Add stricter limits (e.g., max 7 days); validate all inputs with libraries like validator.
- **Sandbox Graceful Degradation (Medium):** Landlock is best-effort; on old kernels, no restrictions applied.
  - **Recommendation:** Log warnings prominently; consider fallback to seccomp or chroot.
- **Web UI JS Security (Medium):** app.js uses Chart.js (embedded); no CSP in code, but server sets it.
  - **Recommendation:** Ensure CSP blocks inline scripts; audit for XSS in dynamic elements.

#### Low Severity
- **Error Leakage (Low):** Some errors (e.g., storage writes) logged but not sanitized for users.
  - **Recommendation:** Use structured logging (e.g., zap); mask sensitive info in responses.
- **Dependency Management (Low):** go.mod not fetched, but minimal deps; ensure regular updates.
  - **Recommendation:** Use dependabot or govulncheck for vuln scanning.

## Recommendations for Improvements

1. **Testing Suite:** Implement comprehensive tests, including fuzzing for parsers and benchmarks for storage.
2. **CI/CD Integration:** Add GitHub Actions for builds, tests, and vuln checks.
3. **Monitoring Extensions:** Add self-metrics export (e.g., Prometheus) for Kula itself.
4. **Config Validation:** Enhance config.Load to validate all fields (e.g., positive durations).
5. **Documentation:** Expand README with architecture diagrams; add CONTRIBUTING.md.

This review is based on fetched source files as of March 06, 2026. Some auxiliary files (e.g., TUI, addons scripts) were partially reviewed. For a full audit, include all commits and dependencies.
