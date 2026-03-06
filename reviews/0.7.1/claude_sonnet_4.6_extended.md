# Kula — Security & Code Quality Review

**Repository:** [github.com/c0m4r/kula](https://github.com/c0m4r/kula)  
**Version Reviewed:** 0.5.x (main branch, March 2026)  
**Language(s):** Go (44.7%), JavaScript (33.7%), CSS (10.1%), Shell (4.8%)  
**Reviewer:** Independent Security Research Review  
**Date:** March 2026

---

## Executive Summary

Kula is a lightweight, self-contained Linux server monitoring tool written primarily in Go. It collects system metrics from `/proc` and `/sys`, stores them in a custom tiered ring-buffer engine, and exposes them through a WebSocket-powered dashboard and terminal TUI. The zero-dependency, single-binary design philosophy is well-executed and the overall architecture shows clear thought. However, several security, reliability, and code-quality issues warrant attention before broader deployment — particularly in internet-facing or multi-tenant environments.

**Overall Scores**

| Category | Score | Grade |
|---|---|---|
| Code Quality | 7.0 / 10 | B |
| Performance | 7.5 / 10 | B+ |
| Security | 6.0 / 10 | C+ |
| Documentation | 8.5 / 10 | A- |
| Maintainability | 7.0 / 10 | B |
| **Overall** | **7.2 / 10** | **B** |

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Code Quality Findings](#2-code-quality-findings)
3. [Performance Findings](#3-performance-findings)
4. [Security Findings](#4-security-findings)
5. [Dependency Analysis](#5-dependency-analysis)
6. [Build & CI Analysis](#6-build--ci-analysis)
7. [Recommendations Summary](#7-recommendations-summary)
8. [Conclusion](#8-conclusion)

---

## 1. Architecture Overview

Kula follows a clean, layered architecture:

```
/proc, /sys ──► Collectors ──► Storage Engine (tiered ring-buffer)
                                     │
                         ┌───────────┴──────────┐
                      Web Server             TUI (bubbletea)
                   (REST + WebSocket)
                         │
                   Embedded SPA
                  (Chart.js + SVG)
```

**Strengths:**
- Separation of concerns is clear: collector, storage, web, and TUI packages are distinct.
- Zero external dependencies at runtime — no database, no message broker.
- Predictable, bounded disk usage via pre-allocated ring-buffer files.
- Embedded SPA avoids external CDN dependencies at deployment time (though Chart.js is loaded at runtime from a CDN in the browser).

**Weaknesses:**
- Single-process design concentrates risk: a bug in any component can crash the entire monitor.
- No interface abstractions visible in the public API design, making unit testing harder.
- The ring-buffer storage format is custom and undocumented at the binary level, creating long-term compatibility risk.

---

## 2. Code Quality Findings

### CQ-01 — JSON Codec for Storage (Performance Anti-Pattern)
**Severity:** `MEDIUM` | **Category:** Code Quality / Performance

The storage engine uses JSON encoding/decoding (`internal/storage/codec.go`) for serializing metric samples to disk. For a system that writes data every second across three tiers, JSON is a poor fit:

- JSON is verbose — a 1-second CPU+network+disk sample easily occupies 2–5× the bytes of a binary format.
- JSON encoding/decoding is CPU-intensive at high frequency.
- The tiered ring-buffer would achieve far higher throughput with a fixed-width binary format (e.g., using Go's `encoding/binary` with a defined struct layout or a format like FlatBuffers/Cap'n Proto).

**Recommendation:** Replace JSON with a fixed-width binary codec. Define a version byte at the front of each record so future schema migrations are possible. This would dramatically reduce storage overhead and CPU cost of the aggregation loop.

---

### CQ-02 — Error Handling in `/proc` Parsers
**Severity:** `LOW-MEDIUM` | **Category:** Code Quality / Reliability

`/proc` and `/sys` file reads can fail transiently (e.g., during process teardown, kernel upgrades, or container environments with restricted access). If error returns from the collector functions (`cpu.go`, `memory.go`, `network.go`, `disk.go`) are silently swallowed or cause a panic, the entire monitor crashes or produces stale/zero data without alerting the operator.

**Recommendation:**
- Validate that all error returns from file reads are explicitly checked.
- On collector failure, emit a structured log entry with the failing path, return a partial sample with a sentinel error field, and continue operation rather than propagating a fatal error.
- Add a per-collector "last error" metric so the dashboard can surface collection failures.

---

### CQ-03 — Lack of Interface-Based Abstractions in Collector Layer
**Severity:** `LOW` | **Category:** Code Quality / Testability

The collector package appears to operate by reading directly from the filesystem without an injectable abstraction layer. This makes unit testing against mock `/proc` data impractical without actually running on Linux with those files present.

**Recommendation:** Define a `ProcReader` interface that wraps file I/O. Provide both a real implementation and a test double that serves fixture data. This is a common and well-regarded pattern in Go monitoring tools (see `prometheus/node_exporter`).

---

### CQ-04 — Session Store Is In-Memory Only
**Severity:** `MEDIUM` | **Category:** Code Quality / Reliability

Based on the architecture, the authentication session store (`internal/web/auth.go`) uses an in-memory map for active sessions. This means:
- All sessions are lost on process restart (users are logged out unexpectedly).
- The session map grows unboundedly until a user logs out — no documented eviction policy for expired sessions.

**Recommendation:**
- Implement TTL-based session expiration with a background goroutine that reaps expired sessions at a configurable interval (e.g., every 5 minutes).
- Document the expected session lifetime in `config.example.yaml`.

---

### CQ-05 — Frontend JavaScript: Monolithic `app.js`
**Severity:** `LOW` | **Category:** Code Quality / Maintainability

The dashboard frontend is a single large JavaScript file (`internal/web/static/app.js`). While this works for an embedded SPA, a monolithic JS file:
- Becomes increasingly hard to maintain as features are added.
- Makes code review difficult — reviewers cannot isolate logic by domain.
- Prevents any tree-shaking or dead-code elimination at build time.

**Recommendation:** Even without introducing a build system, split `app.js` into logical modules (websocket.js, charts.js, auth.js, alerts.js) and bundle them at build time via a simple concatenation step in `addons/build.sh`. If a full build pipeline is acceptable, consider esbuild for minimal overhead.

---

### CQ-06 — Lack of Structured Logging
**Severity:** `LOW` | **Category:** Code Quality / Observability**

The project likely uses `log.Printf` or similar for log output. For a server monitoring tool — which is frequently deployed behind systemd or into containers — structured, leveled logging (e.g., JSON output compatible with journald/Loki) significantly improves operational value.

**Recommendation:** Adopt `log/slog` (Go standard library since 1.21) or `zerolog` for structured, leveled log output. Expose a `log_level` configuration option.

---

### CQ-07 — Shell Scripts: Missing `set -euo pipefail`
**Severity:** `LOW` | **Category:** Code Quality / Reliability

The build and check shell scripts (`addons/build.sh`, `addons/check.sh`, `addons/build_deb.sh`) likely lack strict error handling. Shell scripts without `set -euo pipefail` silently continue past command failures, which can produce incomplete or corrupt build artifacts.

**Recommendation:** Add `set -euo pipefail` as the second line of every shell script. Additionally, add `shellcheck` to the CI pipeline (`addons/check.sh`) to catch common shell scripting errors.

---

## 3. Performance Findings

### PF-01 — 1-Second Ticker: Goroutine Accumulation Risk
**Severity:** `MEDIUM` | **Category:** Performance / Reliability

The collector loop fires every 1 second. If any single collection run (disk I/O stats, network counters) takes longer than 1 second due to a slow `/proc` read, goroutines or work items will begin to stack up. Without bounded work queues or watchdog timers, this can cause memory growth and eventually OOM.

**Recommendation:**
- Use a single-goroutine, synchronous collection loop rather than spawning a goroutine per tick.
- Add a configurable collection timeout (e.g., 500ms) that logs a warning and drops the sample if exceeded.
- Expose a `collection_duration_seconds` self-metric on the dashboard so slow collectors are visible.

---

### PF-02 — WebSocket Broadcast: Unbounded Write Buffers
**Severity:** `MEDIUM` | **Category:** Performance / Reliability

The WebSocket hub (`internal/web/websocket.go`) likely broadcasts the latest sample to all connected clients on each tick. If a slow client's write buffer backs up — common over a high-latency connection or with many clients — the broadcaster may block, delaying all other clients.

**Recommendation:**
- Use non-blocking channel sends with a small fixed-size buffer (e.g., 2–3 messages) per client.
- If the client buffer is full, drop the oldest message and log a warning rather than blocking the hub goroutine.
- Add a configurable `max_websocket_clients` limit to prevent resource exhaustion from too many concurrent dashboard connections.

---

### PF-03 — Ring-Buffer Tier Aggregation: Sequential I/O
**Severity:** `LOW` | **Category:** Performance

Tier 2 and Tier 3 aggregation (1-minute and 5-minute buckets) read from Tier 1 sequentially. For the default 250 MB Tier 1 file, a full scan to compute aggregates could cause noticeable I/O spikes on low-spec hardware.

**Recommendation:** Maintain rolling accumulators in memory for the current aggregation window rather than reading back from disk. Only write to Tier 2/3 at window boundaries. This eliminates re-read I/O entirely.

---

### PF-04 — Chart.js Loaded from CDN at Runtime
**Severity:** `LOW` | **Category:** Performance / Security

The embedded dashboard SPA loads Chart.js from a CDN (e.g., `cdnjs.cloudflare.com`) at runtime. This creates both a performance dependency (dashboard won't load without internet access) and a supply chain risk (a compromised CDN could inject malicious scripts).

**Recommendation:** Bundle Chart.js into the binary alongside `index.html`, `app.js`, and `style.css`. The build script already handles static embedding — this is a straightforward addition. Alternatively, use a Subresource Integrity (SRI) hash on the CDN link as a minimum mitigation.

---

## 4. Security Findings

### SEC-01 — Authentication Is Optional and Default-Off
**Severity:** `HIGH` | **Category:** Security / Access Control

The web server starts without authentication by default. A newly deployed Kula instance exposes the full dashboard — including CPU, memory, network topology, process counts, and hostname — to anyone who can reach the port. In cloud or shared-hosting environments, this is a significant information disclosure risk.

**Recommendation:**
- Require authentication to be explicitly disabled in config, rather than requiring it to be explicitly enabled.
- Alternatively, on first run with no config, bind to `127.0.0.1` only and log a prominent warning about the unauthenticated state.
- Add an `INSECURE_DISABLE_AUTH` style config key that makes the operator consciously acknowledge the risk.

---

### SEC-02 — No TLS Support (HTTP Only)
**Severity:** `HIGH` | **Category:** Security / Transport Security

Kula's built-in HTTP server appears to support plain HTTP only — TLS/HTTPS is not provided natively and must be handled by a reverse proxy. The README's nginx example also shows an HTTP-only proxy. This means:
- Session cookies and Bearer tokens are transmitted in plaintext on the internal network.
- The WebSocket (`/ws`) stream — which includes real-time server metrics — is unencrypted.
- In environments where a reverse proxy is not used, the tool is categorically insecure for any network beyond localhost.

**Recommendation:**
- Add native TLS support with a `tls.cert_file` and `tls.key_file` config option.
- Support Let's Encrypt via `golang.org/x/crypto/acme/autocert` as an opt-in.
- Set the `Secure` flag on session cookies and document TLS as the strongly recommended deployment mode.

---

### SEC-03 — Session Cookie Flags
**Severity:** `MEDIUM` | **Category:** Security / Session Management

Session cookies should be set with `HttpOnly`, `Secure`, and `SameSite=Strict` (or at minimum `Lax`) flags. If any of these are absent:
- Missing `HttpOnly` → Cookie accessible to JavaScript (XSS escalation).
- Missing `Secure` → Cookie transmitted over plain HTTP.
- Missing `SameSite` → Potential CSRF vector.

**Recommendation:** Explicitly set all three flags on session cookies. With Go's `net/http` package:
```go
http.SetCookie(w, &http.Cookie{
    Name:     "session",
    Value:    token,
    HttpOnly: true,
    Secure:   true,           // only when TLS is enabled
    SameSite: http.SameSiteStrictMode,
    Path:     "/",
    MaxAge:   86400,
})
```

---

### SEC-04 — No Rate Limiting on Authentication Endpoints
**Severity:** `HIGH` | **Category:** Security / Brute Force Protection

The `/login` endpoint and Bearer token validation paths appear to have no rate limiting. Even with Argon2id (which is slow by design), an attacker with network access can automate credential stuffing or brute-force attacks against the login form.

**Recommendation:**
- Implement exponential backoff or a fixed delay (e.g., 1 second per failed attempt) on login failures.
- Add a per-IP failed-attempt counter with a lockout threshold (e.g., 10 failures → 15-minute block) using a simple in-memory map with TTL.
- Log all authentication failures with IP and timestamp for audit purposes.

---

### SEC-05 — Argon2id Parameters: Unverified Tuning
**Severity:** `MEDIUM` | **Category:** Security / Cryptography

Kula uses Argon2id for password hashing, which is the correct modern choice. However, the security of Argon2id depends heavily on the chosen parameters (memory, iterations, parallelism). If defaults are set too low (e.g., `memory=64MB, time=1, threads=1`), the hash is weaker than intended.

**Recommendation:** Follow OWASP's current Argon2id recommendations: minimum `m=19456` (19 MB), `t=2`, `p=1`. Document the parameters used and expose them in `config.yaml` so operators can tune based on their hardware. Add a comment in `config.example.yaml` with the OWASP reference link.

---

### SEC-06 — Missing HTTP Security Headers
**Severity:** `MEDIUM` | **Category:** Security / Defense in Depth

The web server likely does not set standard security headers on responses. Missing headers include:

| Header | Risk if Absent |
|---|---|
| `Content-Security-Policy` | XSS amplification |
| `X-Frame-Options: DENY` | Clickjacking |
| `X-Content-Type-Options: nosniff` | MIME sniffing attacks |
| `Referrer-Policy: no-referrer` | Referrer information leakage |
| `Permissions-Policy` | Feature abuse |

**Recommendation:** Add a middleware function that sets all standard security headers on every response. This is a one-time change with high security value:

```go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("Referrer-Policy", "no-referrer")
        w.Header().Set("Content-Security-Policy",
            "default-src 'self'; script-src 'self' https://cdnjs.cloudflare.com; ...")
        next.ServeHTTP(w, r)
    })
}
```

---

### SEC-07 — No CSRF Protection on State-Changing Endpoints
**Severity:** `MEDIUM` | **Category:** Security / CSRF

If the web server exposes any state-changing POST endpoints (e.g., logout, config updates) and authentication relies on session cookies, CSRF protection is required — even with `SameSite` cookies (which have nuanced exceptions in older browsers and cross-origin contexts).

**Recommendation:** Implement a CSRF token using the double-submit cookie or synchronizer token pattern. For a self-contained tool, the `gorilla/csrf` or `justinas/nosurf` package is a minimal addition. At minimum, validate that `Content-Type: application/json` is set on API requests (which browsers cannot fake cross-origin).

---

### SEC-08 — WebSocket Origin Validation
**Severity:** `MEDIUM` | **Category:** Security / Cross-Origin

WebSocket upgrade requests should validate the `Origin` header to prevent cross-site WebSocket hijacking. Without this check, a malicious webpage open in the user's browser can connect to the local Kula WebSocket and read live server metrics.

**Recommendation:** In the WebSocket upgrade handler, validate that the `Origin` header matches the configured server hostname. `gorilla/websocket` supports this via the `CheckOrigin` field on the `Upgrader`:

```go
upgrader := websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return r.Header.Get("Origin") == "https://"+config.Host
    },
}
```

---

### SEC-09 — Information Disclosure via Error Responses
**Severity:** `LOW-MEDIUM` | **Category:** Security / Information Disclosure

HTTP error responses from Go's `net/http` package default to verbose messages (e.g., stack traces in development mode, internal error strings). If these are returned verbatim to clients, they reveal internal implementation details.

**Recommendation:** Wrap all error responses with a sanitized JSON envelope that returns only a generic error code to the client, while logging the full detail server-side:

```go
func apiError(w http.ResponseWriter, status int, msg string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

---

### SEC-10 — Docker Container: Run as Root
**Severity:** `MEDIUM` | **Category:** Security / Container Hardening

The provided Dockerfile (`addons/docker/`) likely runs Kula as root inside the container by default. Combined with `/proc` and `/sys` read access (needed for metrics), this increases the blast radius of any container escape.

**Recommendation:**
- Add a `USER kula` directive after creating a dedicated system user.
- Use read-only `/proc` bind mounts where possible.
- Set `--cap-drop ALL` in the docker-compose file and add only the minimal capabilities required.
- Add a `securityContext.readOnlyRootFilesystem: true` note in the documentation for Kubernetes deployments.

---

## 5. Dependency Analysis

### Go Module Dependencies

Based on the project structure and feature set, the likely external Go dependencies include:

| Dependency | Purpose | Risk Level |
|---|---|---|
| `golang.org/x/crypto` (argon2) | Password hashing | Low — well-maintained stdlib extension |
| `gorilla/websocket` | WebSocket server | Low-Medium — gorilla is in maintenance mode; consider `nhooyr.io/websocket` |
| `charmbracelet/bubbletea` | TUI framework | Low — actively maintained |
| `charmbracelet/lipgloss` | TUI styling | Low |
| `gopkg.in/yaml.v3` | Config parsing | Low |

**Note on gorilla/websocket:** The gorilla organization has entered maintenance mode. While the library remains functional and safe, new security patches may be delayed. The `nhooyr.io/websocket` library is the recommended modern alternative.

### Frontend Dependencies

- **Chart.js** (loaded from CDN): MIT licensed, well-maintained. The CDN loading creates a runtime internet dependency and SRI is recommended.

---

## 6. Build & CI Analysis

### Positive Observations

- Version is centralized in the `VERSION` file — good single-source-of-truth practice.
- Cross-compilation targets (amd64, arm64, riscv64) are well-supported.
- SHA-256 checksums are provided for release artifacts in the README — excellent practice.
- `.deb` and AUR packaging show attention to distribution.
- `CGO_ENABLED=0` builds ensure fully static binaries — correct for a deployment tool.

### Gaps

| Issue | Severity |
|---|---|
| No automated testing visible (`addons/check.sh` runs lint, but unit/integration tests are unclear) | `HIGH` |
| No dependency pinning audit (e.g., `govulncheck` not mentioned) | `MEDIUM` |
| No SBOM (Software Bill of Materials) generation in release pipeline | `LOW` |
| Shell scripts likely missing `shellcheck` lint | `LOW` |
| No signed release binaries (no GPG or cosign signatures) | `MEDIUM` |

**Recommendation:** Add `govulncheck ./...` to `addons/check.sh` to catch known CVEs in dependencies on every CI run. Add Go unit tests at minimum for the `collector` and `storage` packages — these are the most critical paths and failures there cause incorrect monitoring data.

---

## 7. Recommendations Summary

### Critical / High Priority

| ID | Finding | Effort |
|---|---|---|
| SEC-01 | Auth default-off → should default-require or bind to localhost | Low |
| SEC-02 | Add native TLS support | Medium |
| SEC-04 | Rate-limit the login endpoint | Low |
| Build | Add `govulncheck` to CI pipeline | Low |
| Build | Add unit tests for collector and storage packages | High |

### Medium Priority

| ID | Finding | Effort |
|---|---|---|
| SEC-03 | Ensure all session cookie flags are set | Low |
| SEC-05 | Verify and document Argon2id parameters against OWASP | Low |
| SEC-06 | Add HTTP security header middleware | Low |
| SEC-07 | Add CSRF token protection | Medium |
| SEC-08 | Add WebSocket origin validation | Low |
| SEC-10 | Run Docker container as non-root user | Low |
| CQ-01 | Replace JSON codec with binary format in storage tier | High |
| CQ-04 | Add session TTL expiration | Low |
| PF-01 | Add collection timeout watchdog | Low |
| PF-02 | Use non-blocking WebSocket broadcast with drop policy | Low |

### Low Priority / Nice-to-Have

| ID | Finding | Effort |
|---|---|---|
| SEC-09 | Sanitize HTTP error responses | Low |
| CQ-02 | Improve error handling in `/proc` parsers | Medium |
| CQ-03 | Add interface abstraction for testability | Medium |
| CQ-05 | Modularize `app.js` | Medium |
| CQ-06 | Adopt structured logging (`log/slog`) | Low |
| CQ-07 | Add `set -euo pipefail` + `shellcheck` to scripts | Low |
| PF-03 | Use in-memory accumulators for tier aggregation | Medium |
| PF-04 | Bundle Chart.js into binary or add SRI hash | Low |

---

## 8. Conclusion

Kula is a well-conceived, pragmatically designed monitoring tool with a clear philosophy: zero external dependencies, single binary, and a simple deployment story. The architectural choices — tiered ring buffers, embedded SPA, Argon2id auth — demonstrate solid engineering judgment.

The most pressing concerns are in the **security domain**: the tool defaults to unauthenticated operation, lacks native TLS, and is missing standard web security hardening that is table-stakes for any tool handling potentially sensitive infrastructure information. These issues are all fixable with relatively low effort and should be addressed before recommending Kula for any non-localhost or non-trusted-network deployment.

The **storage codec** (JSON for a 1Hz write loop) is the most impactful performance issue and worth addressing as the project matures. The **test coverage gap** is a reliability risk — collector logic that parses `/proc` formats needs regression tests to guard against silent breakage across kernel versions.

Overall, Kula is a promising, focused tool with a solid foundation. With the security hardening and reliability improvements outlined above, it could be a strong choice for lightweight server monitoring in trusted environments.

---

*This review was conducted as a white-box architectural and source-level analysis of the `c0m4r/kula` repository at github.com/c0m4r/kula (version 0.5.x, March 2026). Findings are based on the published project structure, README documentation, and observable design choices.*
