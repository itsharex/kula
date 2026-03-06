 I'll analyze this repository for you. Let me start by examining the code structure and content.  
 Let me try searching for information about this repository:  
  Now let me access the actual kula repository:    
  Let me get the main source code files:    
  Let me try to access the repository files directly:    
  Let me get more critical files:    
  Let me get more files to complete the analysis:    
  Now I have enough information to write a comprehensive code review report. Let me compile my findings.

---

# Kula Code Review Report

**Repository:** https://github.com/c0m4r/kula  
**Project:** Kula-Szpiegula - Lightweight Linux Server Monitoring Tool  
**Language:** Go (v1.25.7)  
**Version Reviewed:** 0.6.0 (main branch)  
**Date:** 2026-03-06  
**Reviewer:** Security Researcher

---

## Executive Summary

Kula is a well-architected, self-contained Linux monitoring tool written in Go. It features a tiered ring-buffer storage engine, real-time WebSocket dashboard, terminal UI, and Linux Landlock sandboxing. The codebase demonstrates solid engineering practices with good separation of concerns, proper use of Go concurrency patterns, and thoughtful performance optimizations.

**Overall Grade: B+ (Good)**

| Category | Score | Grade |
|----------|-------|-------|
| Code Quality | 8.5/10 | A- |
| Performance | 9/10 | A |
| Security | 7.5/10 | B+ |
| **Overall** | **8.3/10** | **B+** |

---

## 1. Code Quality Analysis

### 1.1 Architecture & Design

**Strengths:**
- **Clean modular structure** with clear separation between `collector/`, `storage/`, `web/`, `tui/`, and `sandbox/` packages
- **Effective use of Go interfaces** and dependency injection patterns
- **Consistent error handling** with wrapped errors using `fmt.Errorf("...: %w", err)`
- **Good documentation** with inline comments explaining complex logic (e.g., aggregation algorithms, tier selection strategies)

**Issues Found:**

| Severity | Location | Issue | Recommendation |
|----------|----------|-------|----------------|
| 🟡 Medium | `config/config.go:82-90` | Path expansion logic for `~` is fragile | Use `os.ExpandEnv()` or proper shell expansion library |
| 🟡 Medium | `storage/tier.go:85-95` | Header corruption handling silently reinitializes data | Log warning before data loss; consider backup mechanism |
| 🟢 Low | Multiple files | Inconsistent receiver naming (`s` vs `t` vs `a`) | Standardize on meaningful receiver names |

**Code Duplication:**
- Aggregation ratio calculation appears in both `store.go:141-153` and `store.go:175-187` - should be extracted to a helper function

### 1.2 Error Handling

**Strengths:**
- Proper use of error wrapping throughout
- Graceful degradation (e.g., Landlock sandbox failure is non-fatal)
- Context-aware timeouts for shutdown operations

**Issues:**

| Severity | Location | Issue |
|----------|----------|-------|
| 🔴 High | `web/server.go:225` | JSON encoding errors logged but not returned to client |
| 🟡 Medium | `web/websocket.go:65` | WebSocket write errors only logged, no client notification |
| 🟡 Medium | `storage/tier.go:104` | `writeHeader()` errors in `Close()` are ignored |

### 1.3 Testing & Maintainability

**Observations:**
- No test files were observed in the retrieved codebase
- Benchmark suite exists (`addons/benchmark.sh`) but no unit tests visible
- Build scripts present for multiple platforms (good CI/CD preparation)

**Score: 8.5/10**

---

## 2. Performance Analysis

### 2.1 Storage Engine

**Excellent Design Choices:**
- **Ring buffer with pre-allocated files** eliminates fragmentation and GC pressure
- **O(1) latest sample retrieval** via in-memory cache (`latestCache`)
- **Tiered aggregation** reduces data volume by 60:1 and 300:1 ratios
- **Buffered I/O** (`bufio.NewReaderSize` with 1MB buffer) for range queries
- **Lazy downsampling** only when samples > 800

**Performance Metrics:**
- Collection interval: 1 second (configurable)
- Memory usage: Bounded by tier buffer sizes (~450MB default disk, minimal RAM)
- Query optimization: Automatic tier selection based on estimated sample count

### 2.2 Concurrency

**Strengths:**
- Lock-free WebSocket broadcasting using channels
- RWMutex for read-heavy operations (tier queries)
- Lock sharding in `AuthManager` (separate mutex for sessions vs rate limiter)

**Potential Issues:**

| Severity | Location | Issue | Impact |
|----------|----------|-------|--------|
| 🟡 Medium | `storage/store.go:75` | `WriteSample` holds write lock during aggregation | May block readers briefly |
| 🟡 Medium | `web/server.go:289` | WebSocket hub uses unbounded `regCh`/`unregCh` | Potential memory growth under connection flood |

### 2.3 Resource Management

**Strengths:**
- `defer` used consistently for resource cleanup
- `sync.Pool` not needed due to pre-allocated buffers
- Proper `Close()`/`Flush()` patterns for file handles

**Score: 9/10**

---

## 3. Security Analysis

### 3.1 Authentication & Authorization

**Strengths:**
- **Argon2id** for password hashing (memory-hard, resistant to GPU cracking)
- **Constant-time comparison** using `subtle.ConstantTimeCompare` (timing attack prevention)
- **Rate limiting** on login attempts (5 attempts per 5 minutes per IP)
- **Secure cookie flags**: `HttpOnly`, `Secure` (TLS detection), `SameSite=Strict`
- **Session timeout** enforcement with periodic cleanup

**Issues:**

| Severity | Location | Issue | CVSS Estimate |
|----------|----------|-------|---------------|
| 🔴 High | `web/auth.go:115-118` | Session tokens generated with only 32 bytes (hex = 256 bits) but no rotation on privilege change | 5.3 (Medium) |
| 🟡 Medium | `web/server.go:189` | `X-Forwarded-For` header trusted without validation | 5.0 (Medium) |
| 🟡 Medium | `web/auth.go:82` | Rate limiter uses IP only, vulnerable to IPv6 rotation | 4.3 (Medium) |
| 🟢 Low | `config.example.yaml:28` | Default `session_timeout: 24h` is long for a monitoring tool | 3.1 (Low) |

### 3.2 Web Security

**Strengths:**
- **Content Security Policy** implemented with strict defaults
- **X-Frame-Options: DENY** prevents clickjacking
- **X-Content-Type-Options: nosniff** prevents MIME sniffing
- **WebSocket origin validation** prevents CSWSH attacks
- **Read limits** on WebSocket messages (4KB)

**Issues:**

| Severity | Location | Issue |
|----------|----------|-------|
| 🟡 Medium | `web/server.go:155` | No `Referrer-Policy` header set |
| 🟡 Medium | `web/server.go:155` | No `Permissions-Policy` header set |
| 🟢 Low | `web/server.go:155` | CSP allows `fonts.googleapis.com` (external dependency) |

### 3.3 Input Validation

**Strengths:**
- Time range validation (max 31 days, inverted range check)
- Size parsing with strict unit validation
- JSON struct tags with proper typing

**Issues:**

| Severity | Location | Issue |
|----------|----------|-------|
| 🔴 High | `web/server.go:185-190` | `X-Forwarded-For` can contain multiple IPs (comma-separated), but only first is used |
| 🟡 Medium | `config/config.go:140-158` | `parseSize()` uses `fmt.Sscanf` which can panic on malformed input |
| 🟡 Medium | `storage/tier.go:220` | No validation that `dataLen` isn't excessively large before allocation |

### 3.4 Sandboxing

**Strengths:**
- **Landlock LSM integration** for filesystem and network restrictions
- Graceful degradation on unsupported kernels
- Minimal attack surface (only `/proc`, `/sys`, config, and storage directory)

**Limitations:**
- Landlock is best-effort; doesn't prevent all attack vectors
- No seccomp-bpf or AppArmor profiles included
- Capabilities not dropped (could run with `CAP_SYS_PTRACE` removed)

### 3.5 Cryptography

**Strengths:**
- `crypto/rand` used for token generation (CSPRNG)
- Argon2id with reasonable parameters (64MB memory, 1 iteration, 4 threads)

**Issues:**

| Severity | Location | Issue |
|----------|----------|-------|
| 🟡 Medium | `web/auth.go:35-38` | Argon2 parameters are hardcoded, not configurable |
| 🟢 Low | `web/auth.go:125` | Session tokens are hex-encoded (inefficient), could use base64url |

**Score: 7.5/10**

---

## 4. Specific Security Findings

### 4.1 WebSocket Origin Check Bypass Risk

**Location:** `web/websocket.go:18-35`

The origin validation logic manually parses the Origin header:

```go
originHost := ""
for i := 0; i < len(origin); i++ {
    if origin[i] == ':' && i+2 < len(origin) && origin[i+1] == '/' && origin[i+2] == '/' {
        originHost = origin[i+3:]
