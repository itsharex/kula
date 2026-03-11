## Approach

I’ll do a full-pass audit across the repo focusing on:
- **Architecture & code quality** (structure, readability, correctness risks)
- **Performance** (hot paths, allocations, IO, concurrency)
- **Security** (input handling, privilege boundaries, file/OS interactions, dependency risks)

I’m going to:
1) Map entrypoints and main data flow (`cmd/`, `internal/`)
2) Review config parsing, collectors, storage/codec, and any OS/syscall interactions
3) Review tests and build scripts for security/lint coverage
4) Produce a structured Markdown report with findings, severity, and concrete fixes

I’ll start by scanning the codebase with repo-wide search and reading key files.

I’ve identified the main runtime flow (`cmd/kula` → `config.Load` → `collector.Collect` loop → `storage.Store` tiered writes → `web.Server` HTTP/WS). Next I’ll inspect the *security-critical* surfaces: web server handlers/auth/session/cookies, websocket hub, sandboxing (Landlock), and storage codec/tier file format—then I’ll consolidate findings into a prioritized audit report.

I attempted to read `internal/web/ws.go` but it doesn’t exist in this repo (likely a different filename). I’ll quickly list `internal/web/` and then read the correct websocket/auth-related files, plus storage codec/tier and sandbox enforcement, to complete the security/perf review.


I’ll now finish scanning the remaining security-relevant files (WebSocket implementation, collectors touching [/proc](cci:9://file:///proc:0:0-0:0)/[/sys](cci:9://file:///sys:0:0-0:0), TUI, and storage query paths), plus `go.mod` and build/test scripts. Then I’ll deliver the comprehensive audit report with prioritized findings and concrete remediation guidance.






# Comprehensive Code Audit Report (Quality, Performance, Security)

## Scope & Method
I reviewed the Go codebase with focus on:
- **Entrypoints & data flow**: `cmd/kula/main.go` → [config.Load](cci:1://file:///home/c0m4r/ai/kula/internal/config/config.go:153:0-190:1) → collection loop ([internal/collector](cci:9://file:///home/c0m4r/ai/kula/internal/collector:0:0-0:0)) → tiered storage (`internal/storage`) → web server + WS ([internal/web](cci:9://file:///home/c0m4r/ai/kula/internal/web:0:0-0:0)) + sandbox (`internal/sandbox`).
- **Security posture**: auth/session design, HTTP hardening, WS origin checks, filesystem permissions, sandboxing, and input handling.
- **Performance & reliability**: I/O patterns, memory allocations, locking, concurrency, and failure modes.
- **Dependencies & tooling**: [go.mod](cci:7://file:///home/c0m4r/ai/kula/go.mod:0:0-0:0), [addons/check.sh](cci:7://file:///home/c0m4r/ai/kula/addons/check.sh:0:0-0:0), [addons/build.sh](cci:7://file:///home/c0m4r/ai/kula/addons/build.sh:0:0-0:0).

Severity labels: **Critical / High / Medium / Low** (context-aware; “Critical” = likely compromise with low effort).

---

# Executive Summary

## What you did well
- **Good default bind**: web listens on `127.0.0.1` by default ([internal/config/config.go](cci:7://file:///home/c0m4r/ai/kula/internal/config/config.go:0:0-0:0)).
- **Landlock sandboxing**: best-effort Landlock restrictions with filesystem + bind restriction ([internal/sandbox/sandbox.go](cci:7://file:///home/c0m4r/ai/kula/internal/sandbox/sandbox.go:0:0-0:0)) is a strong hardening step for a monitoring daemon.
- **Reasonable HTTP security headers**: CSP + `X-Frame-Options` + `nosniff` ([internal/web/server.go](cci:7://file:///home/c0m4r/ai/kula/internal/web/server.go:0:0-0:0)).
- **Session tokens stored hashed-at-rest**: sessions are stored using `SHA-256(token)` rather than raw token ([internal/web/auth.go](cci:7://file:///home/c0m4r/ai/kula/internal/web/auth.go:0:0-0:0)), reducing disk disclosure impact.
- **Ring-buffer tier format with `0600`**: tier files created as `0600` and session file as `0600` are good defaults.

## Top issues to fix first
1. **High**: `/api/login` returns the **session token in JSON** *in addition to* setting an HttpOnly cookie. This increases token exposure risk via logs, extensions, devtools, reverse proxies, and XSS in any consuming context.
2. **High**: HTTP server lacks **timeouts** (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`). This enables slowloris-style resource exhaustion.
3. **High**: CSP nonce is generated but **not applied to any script tag** in embedded static HTML, so CSP is likely either ineffective or breaks scripts depending on how the HTML is authored.
4. **Medium**: `TrustProxy` handling trusts `X-Forwarded-For` without validating chain depth/known proxy IPs; it’s safe only when you *guarantee* a trusted reverse proxy in front.
5. **Medium**: Storage directory created with `0755` in [NewStore](cci:1://file:///home/c0m4r/ai/kula/internal/storage/store.go:46:0-84:1), but sandbox uses `0750`. This mismatch can broaden access beyond intended.

---

# Architecture & Data Flow

## Runtime
- **Daemon mode**: `cmd/kula/main.go` `serve`:
  - Loads config ([config.Load](cci:1://file:///home/c0m4r/ai/kula/internal/config/config.go:153:0-190:1))
  - Initializes collector (`collector.New`)
  - Initializes store ([storage.NewStore](cci:1://file:///home/c0m4r/ai/kula/internal/storage/store.go:46:0-84:1))
  - Applies Landlock sandbox ([sandbox.Enforce](cci:1://file:///home/c0m4r/ai/kula/internal/sandbox/sandbox.go:22:0-88:1))
  - Starts HTTP/WS server ([web.NewServer](cci:1://file:///home/c0m4r/ai/kula/internal/web/server.go:39:0-49:1) / [Start](cci:1://file:///home/c0m4r/ai/kula/internal/web/server.go:150:0-222:1))
  - Starts a ticker loop to collect and persist samples and broadcast over WS.

## Interfaces / “Attack Surface”
- HTTP endpoints (local by default):
  - `/api/current`, `/api/history`, `/api/config`, `/api/login`, `/api/logout`, `/api/auth/status`, `/ws`, plus static files `/`.
- File I/O:
  - Reads from [/proc](cci:9://file:///proc:0:0-0:0), [/sys](cci:9://file:///sys:0:0-0:0)
  - Writes to tier files under storage dir
  - Writes `sessions.json` (if auth enabled)

---

# Security Review

## 1) Authentication & Session Management

### Findings
- **[High] Token returned in login response**
  - [handleLogin](cci:1://file:///home/c0m4r/ai/kula/internal/web/server.go:401:0-448:1) sets cookie `kula_session` **and** responds with `{"token": token}`.
  - Risk: token leakage through:
    - browser devtools persistence, JS access (even if cookie is HttpOnly, the JSON is not)
    - reverse-proxy access logs (if response bodies logged)
    - clients accidentally logging responses
- **[Medium] Session “fingerprinting” can cause lockouts**
  - Session binds to **IP + User-Agent** ([ValidateSession](cci:1://file:///home/c0m4r/ai/kula/internal/web/auth.go:148:0-172:1)).
  - Users behind NAT, mobile networks, or some proxies may see IP changes → forced logout.
  - Security benefit is limited; token theft is already mitigated by HttpOnly cookie + origin checks; binding IP can become a DoS on usability.
- **[Medium] Rate limiter is in-memory only**
  - On restart, brute-force window resets.
  - Still helpful, but don’t overestimate protection.
- **[Low] Argon2 params**
  - Default `Time:1, Memory: 64MB, Threads:4`. This is acceptable for many environments, but 64MB may be low for high-security deployments; needs environment-specific tuning.

### Recommendations
- **[High] Remove token from JSON response** for browser usage; rely on cookie.
  - If you need API-token mode for CLI, gate it behind config (e.g., `auth.allow_bearer_token`) and/or require explicit header (`Accept: application/token`) and never enable by default.
- Consider dropping IP-binding; keep UA binding or neither, and rely on short session duration + rotation.
- Consider storing **only session metadata** on disk (you already store hashed token), and optionally encrypt at rest if threat model includes local disk readers.

---

## 2) HTTP Security Controls

### Findings
- **[High] No server timeouts**
  - `s.httpSrv = &http.Server{Handler: handler}` without timeouts.
  - Enables slowloris / connection hoarding.
- **[Medium] CSP nonce generated but not used**
  - [securityMiddleware](cci:1://file:///home/c0m4r/ai/kula/internal/web/server.go:137:0-148:1) sets CSP with `'nonce-<nonce>'` but doesn’t provide nonce to templates/static content.
  - Because static HTML is embedded, scripts likely lack nonce. This can either:
    - break scripts (if CSP enforced by browser)
    - or lead you to loosen CSP later (common pitfall)
- **[Medium] No HSTS**
  - If served behind TLS, you can add `Strict-Transport-Security`. (Only safe when you’re sure it’s always HTTPS.)
- **[Low] `connect-src` allows `ws:` generally**
  - CSP: `connect-src 'self' ws: wss:;` allows WS to any host. Better to restrict to `'self'` and `wss:` if possible.
- **[Low] Error bodies sometimes not strictly JSON**
  - e.g., `http.Error(w, fmt.Sprintf(\`{"error":"%s"}\`, err), ...)` might embed unescaped quotes/newlines from `err`.

### Recommendations
- Add at least:
  - `ReadHeaderTimeout: 5s`
  - `ReadTimeout: 15s`
  - `WriteTimeout: 15s`
  - `IdleTimeout: 60s`
- If you keep nonce CSP, you need a way to inject nonce into HTML responses (hard with `http.FileServer` on embedded assets). Alternative:
  - remove nonce and use **hash-based CSP** for known scripts
  - or serve a small dynamic HTML wrapper (still embedded) that injects nonce.
- Consider returning structured JSON errors by using `json.NewEncoder` and proper escaping.

---

## 3) WebSocket Security

### Findings
- **[Good] CSWSH mitigation**:
  - Gorilla `CheckOrigin` validates `Origin` host matches `r.Host`.
  - Allows no `Origin` (non-browser clients) — OK, because browsers always send `Origin` for WS.
- **[Medium] `r.Host` vs reverse proxy**
  - Behind a proxy, `Host` may be altered. If `TrustProxy` is enabled, you still don’t validate `X-Forwarded-Host`, so origin checks could fail or behave unexpectedly.
- **[Low] Hub concurrency correctness**
  - `wsHub.clients` guarded by RWMutex, and reg/unreg via channels. Broadcast iterates map under RLock and pushes into per-client channel non-blocking (good).

### Recommendations
- If behind reverse proxy, clearly document required headers and set `upgrader.CheckOrigin` to validate against a configured external host or derived trusted host list.
- Consider setting `conn.SetReadDeadline` in the main goroutine too (currently only in read pump goroutine), but current pattern is reasonable given a dedicated read goroutine exists.

---

## 4) Landlock Sandbox

### Findings
- **[Good] BestEffort strategy**: gracefully degrades on unsupported kernels.
- **[Medium] Coverage mismatch**
  - Sandbox allows [/proc](cci:9://file:///proc:0:0-0:0), [/sys](cci:9://file:///sys:0:0-0:0), config file, storage dir, bind TCP port.
  - Collector also reads [/etc/os-release](cci:7://file:///etc/os-release:0:0-0:0) and [/proc/sys/kernel/osrelease](cci:7://file:///proc/sys/kernel/osrelease:0:0-0:0) in [cmd/kula/system_info.go](cci:7://file:///home/c0m4r/ai/kula/cmd/kula/system_info.go:0:0-0:0).
  - Landlock rules do **not** include [/etc/os-release](cci:7://file:///etc/os-release:0:0-0:0). If sandbox enforced before reading OS name, it would break; currently OS name read happens **before** [sandbox.Enforce](cci:1://file:///home/c0m4r/ai/kula/internal/sandbox/sandbox.go:22:0-88:1) in `runServe`, so it’s okay.
- **[Low] Directory permissions mismatch**
  - Sandbox creates storage dir with `0750`, while store uses `0755`.

### Recommendations
- Align storage dir perms (prefer `0750` if single-user service).
- Consider allowing read-only access to [/etc/os-release](cci:7://file:///etc/os-release:0:0-0:0) only if you ever move OS-name reads after sandbox enforcement.

---

## 5) Storage / File Format Security

### Findings
- **[Medium] JSON parsing “fast path” is fragile**
  - [extractTimestamp](cci:1://file:///home/c0m4r/ai/kula/internal/storage/codec.go:32:0-46:1) searches for the literal substring `"ts":"`.
  - JSON encoding order and spacing is usually stable with Go’s `encoding/json` given struct tags, but it is not a formal guarantee across future refactors.
  - If this fails, code falls back to full decode (safe), but performance could regress unexpectedly.
- **[Low] Partial/corrupted records**
  - On wrap, you write a zero-length sentinel to mark end-of-segment (good).
  - Reads break on invalid lengths, which is robust, but may cause data loss on minor corruption (acceptable for monitoring, but note it).

### Recommendations
- If you want a robust fast timestamp extraction, consider:
  - a tiny custom binary framing with timestamp in prefix, or
  - JSON decode into a small struct `{Ts time.Time}` first, then full decode only when needed (still a decode, but cheaper and stable).

---

## 6) Privacy / Data Exposure

### Findings
- `/api/config` returns OS/kernel/arch/hostname depending on `global.show_system_info`.
- If the web UI is ever exposed beyond localhost, this is fingerprinting data.
- Default is reasonable; risk grows when `listen` is set to `0.0.0.0` or `""`.

### Recommendations
- If `web.listen` is non-loopback, consider:
  - forcing auth enabled by default
  - warning loudly in logs at startup
  - option to disable `/api/config` details entirely.

---

# Performance & Reliability Review

## 1) HTTP server & concurrency
- **[High] Missing HTTP timeouts** is both security and reliability.
- [Start()](cci:1://file:///home/c0m4r/ai/kula/internal/web/server.go:150:0-222:1) creates two listeners for dual-stack (when `listen == ""`) and serves them; it returns first error from `errCh`. If one listener fails quickly, it exits even if the other is serving. This may be acceptable but can be surprising.
- The collector loop runs in a goroutine and continues until context done; store writes are guarded by mutex.

## 2) Storage performance
- Tier reads use `io.NewSectionReader` + `bufio.NewReaderSize(1MB)` for scanning: good.
- There are **per-record allocations** in reads:
  - `lenBuf := make([]byte, 4)` in tight loops
  - `data := make([]byte, dataLen)` per record
- This is okay for moderate sizes but could become heavy with large history reads.

## 3) Collector performance
- CPU temperature sensor discovery is cached (`sysTempSensors`), good.
- Collector reads [/proc](cci:9://file:///proc:0:0-0:0) and [/sys](cci:9://file:///sys:0:0-0:0) directly; complexity is mostly O(#cores + #ifs + #disks + #mounts).

## Recommendations
- Reuse small buffers in tier scan loops (e.g. `var lenBuf [4]byte`) to reduce GC churn.
- Consider adding an upper bound on `points` parsing: currently `Sscanf` without strict validation; you clamp later via `maxScreenPoints`, good.
- Consider making [Store.Close()](cci:1://file:///home/c0m4r/ai/kula/internal/storage/store.go:371:0-381:1) best-effort (close all tiers even if one fails) if you care about shutdown robustness.

---

# Code Quality Review

## Strengths
- Clear package separation: [collector](cci:9://file:///home/c0m4r/ai/kula/internal/collector:0:0-0:0), `storage`, [web](cci:9://file:///home/c0m4r/ai/kula/internal/web:0:0-0:0), `sandbox`, `config`.
- Tests exist in collector/storage/config.
- Use of `sync.RWMutex` and contexts is mostly correct.

## Issues / smells
- Some mixed responsibilities:
  - [web/server.go](cci:7://file:///home/c0m4r/ai/kula/internal/web/server.go:0:0-0:0) contains API handlers, middleware, WS hub, and IP extraction; it’s workable but becoming “god file”.
- Error handling sometimes returns JSON-ish strings via `http.Error` without escaping.

---

# Dependency & Tooling Review

## Dependencies (from [go.mod](cci:7://file:///home/c0m4r/ai/kula/go.mod:0:0-0:0))
- `github.com/gorilla/websocket v1.5.3`: widely used; keep it updated.
- `golang.org/x/crypto v0.48.0`, `x/sys v0.42.0`: good modern baselines.
- `github.com/landlock-lsm/go-landlock v0.7.0`: good hardening.

## Tooling scripts
- [./addons/check.sh](cci:7://file:///home/c0m4r/ai/kula/addons/check.sh:0:0-0:0) runs:
  - `govulncheck` (if installed)
  - `go vet`
  - `go test -race`
  - `golangci-lint`
  Good coverage.
- [./addons/build.sh](cci:7://file:///home/c0m4r/ai/kula/addons/build.sh:0:0-0:0) cross builds with `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`: good for reproducibility and reduced info leakage.

---

# Prioritized Findings & Fix List

## Critical
- None found that are trivially remotely exploitable *given the default listen on localhost and auth optional*. If you expose it publicly, several High items become closer to Critical.

## High
- **[High] `/api/login` returns token in JSON**
  - Fix: stop returning token; rely on HttpOnly cookie (or make token-return optional and off by default).
- **[High] Missing HTTP timeouts**
  - Fix: set `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`.
- **[High] CSP nonce not actually wired**
  - Fix: either implement dynamic HTML nonce injection or switch to hash-based CSP / remove nonce directive.

## Medium
- **[Medium] `TrustProxy` too permissive**
  - Fix: support “trusted proxy IP ranges” and only honor `X-Forwarded-For` when `RemoteAddr` is trusted; otherwise ignore.
- **[Medium] Storage dir perms mismatch (`0755` vs `0750`)**
  - Fix: tighten to `0750` consistently (or configurable).
- **[Medium] Fragile timestamp extraction**
  - Fix: stable partial decode or binary prefix.

## Low
- `http.Error` JSON escaping issues
- HSTS header missing (when HTTPS)
- WS origin behavior behind reverse proxies could surprise

---

# Recommended Hardening Configuration

If you ever bind to non-loopback (`0.0.0.0`, `""`, public IP):
- **Enable auth** and require strong Argon2 params
- Put behind a **reverse proxy with TLS**
- Enable `TrustProxy` only when you restrict the service to the proxy’s private network and validate proxy IPs
- Consider firewalling to admin networks only

---

# Status / Next Steps

## What’s complete
- Full codebase scan of the major components ([cmd/](cci:9://file:///home/c0m4r/ai/kula/cmd:0:0-0:0), `internal/config`, [internal/collector](cci:9://file:///home/c0m4r/ai/kula/internal/collector:0:0-0:0), `internal/storage`, [internal/web](cci:9://file:///home/c0m4r/ai/kula/internal/web:0:0-0:0), `internal/sandbox`) and build/test scripts.
- Prioritized security/performance/code-quality report with concrete remediation items.

## If you want, I can implement fixes
If you tell me whether you prefer “secure-by-default even if it breaks some deployments” vs “minimal behavior change”, I can prepare a patch that:
- Adds HTTP server timeouts
- Stops returning login token by default (with optional CLI token mode)
- Improves CSP strategy for embedded static assets
- Tightens `TrustProxy` logic

**Task completion status:** Audit/report delivered.
