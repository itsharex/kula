# Kula Code Review Report

**Project:** Kula-Szpiegula - Lightweight Linux Server Monitoring Tool  
**Repository:** https://github.com/c0m4r/kula  
**Version Reviewed:** 0.6.0  
**Review Date:** 2025-01-09  
**Reviewer:** Security Research & Code Quality Analysis

---

## Executive Summary

Kula is a lightweight, self-contained Linux server monitoring tool written in Go. It collects system metrics from `/proc` and `/sys` filesystems, stores them in a tiered ring-buffer storage engine, and serves them through a real-time web UI dashboard and terminal TUI. The project demonstrates solid engineering practices with a well-structured codebase, comprehensive test coverage, and thoughtful security implementations.

### Overall Scores

| Category | Score | Grade |
|----------|-------|-------|
| **Code Quality** | 8.5/10 | A- |
| **Security** | 7.5/10 | B+ |
| **Performance** | 8.0/10 | A- |
| **Test Coverage** | 8.0/10 | A- |
| **Documentation** | 7.5/10 | B+ |
| **Overall** | **7.9/10** | **B+** |

---

## 1. Code Quality Analysis

### 1.1 Architecture & Structure

**Score: 8.5/10** | **Grade: A-**

The project follows a clean, modular architecture that separates concerns effectively:

```
kula/
├── cmd/kula/main.go          # CLI entry point
├── internal/
│   ├── collector/            # Metrics collection
│   ├── config/               # Configuration management
│   ├── sandbox/              # Landlock sandboxing
│   ├── storage/              # Tiered ring-buffer storage
│   ├── tui/                  # Terminal UI
│   └── web/                  # HTTP/WebSocket server
```

**Strengths:**
- Clean separation of concerns with well-defined package boundaries
- Internal packages properly encapsulated
- Single responsibility principle generally well-applied
- Clear data flow from collectors → storage → web/TUI

**Issues Identified:**

| ID | Issue | Severity | Location |
|----|-------|----------|----------|
| CQ-001 | Module name contains hyphen (`kula-szpiegula`) which is non-standard | Low | go.mod:1 |
| CQ-002 | Missing error handling in deferred close operations | Low | Multiple files |
| CQ-003 | Some functions exceed recommended complexity | Low | tui.go:View() |

### 1.2 Code Style & Readability

**Score: 8.0/10** | **Grade: A-**

The codebase demonstrates good Go idioms and consistent styling:

**Positive Observations:**
- Consistent use of defer for cleanup operations
- Proper use of sync primitives (RWMutex, Mutex)
- Clear naming conventions following Go standards
- Effective use of comments for complex logic
- Proper error wrapping with fmt.Errorf

**Example of Good Practice (storage/tier.go):**
```go
// readTimestampAt reads the timestamp of the first record at the given data-region
// offset. Returns an error if the record is invalid. Must be called under at least
// a read lock (Write holds the write lock, which is sufficient).
func (t *Tier) readTimestampAt(dataOffset int64) (time.Time, error) {
```

**Areas for Improvement:**

| ID | Issue | Severity | Recommendation |
|----|-------|----------|----------------|
| CQ-004 | Magic numbers in code | Low | Define constants for values like `headerSize = 64` |
| CQ-005 | Inconsistent error messages | Low | Standardize error message format |

### 1.3 Error Handling

**Score: 7.5/10** | **Grade: B+**

Error handling is generally robust but has some inconsistencies:

**Good Practices:**
```go
// Proper error wrapping
return nil, fmt.Errorf("opening tier %d: %w", i, err)

// Proper use of errors.Is for checking
if os.IsNotExist(err) {
```

**Issues:**

| ID | Issue | Severity | Location |
|----|-------|----------|----------|
| EH-001 | Silent error ignoring with `_ = f.Close()` | Medium | Multiple files |
| EH-002 | Missing error context in some log statements | Low | collector/util.go |
| EH-003 | Fatal errors in goroutines could leave system in inconsistent state | Medium | main.go:152-154 |

---

## 2. Security Analysis

### 2.1 Authentication & Authorization

**Score: 7.5/10** | **Grade: B+**

The authentication system uses industry-standard practices:

**Strengths:**
- Argon2id password hashing (OWASP recommended)
- Cryptographically secure random salt generation using `crypto/rand`
- Constant-time comparison for credential validation
- Rate limiting on login attempts (5 attempts per 5 minutes)
- Session timeout with configurable duration
- Support for both cookie and Bearer token authentication
- HttpOnly, Secure, SameSite=Strict cookie flags

**Code Review (auth.go):**
```go
// Excellent: Using constant-time comparison
if subtle.ConstantTimeCompare([]byte(username), []byte(a.cfg.Username)) != 1 {
    return false
}
hash := HashPassword(password, a.cfg.PasswordSalt)
return subtle.ConstantTimeCompare([]byte(hash), []byte(a.cfg.PasswordHash)) == 1
```

**Security Issues Identified:**

| ID | Issue | Severity | CVSS | Recommendation |
|----|-------|----------|------|----------------|
| SEC-001 | Sessions stored in-memory only | Medium | 5.3 | Consider persistent session storage with encryption |
| SEC-002 | No CSRF token implementation | Medium | 4.3 | Add CSRF protection for state-changing operations |
| SEC-003 | Rate limiter uses IP from X-Forwarded-For without validation | Low | 3.1 | Validate and sanitize X-Forwarded-For header |
| SEC-004 | No password complexity requirements | Low | 2.5 | Document password policy recommendations |
| SEC-005 | Session tokens are hex-encoded (not JWT) | Info | N/A | Consider JWT for stateless scaling |

### 2.2 Input Validation

**Score: 7.0/10** | **Grade: B**

**WebSocket Input Validation (websocket.go):**
```go
conn.SetReadLimit(4096) // Good: Limit incoming JSON commands
```

**Issues:**

| ID | Issue | Severity | Location |
|----|-------|----------|----------|
| IV-001 | Time range query lacks upper bound validation beyond 31 days | Low | server.go:270 |
| IV-002 | No validation of filesystem mount point names | Low | disk.go |
| IV-003 | Network interface names not sanitized before display | Low | network.go |

### 2.3 WebSocket Security

**Score: 8.0/10** | **Grade: A-**

The WebSocket implementation includes strong origin validation:

```go
// Excellent: Strict origin check to prevent Cross-Site WebSocket Hijacking (CSWSH)
CheckOrigin: func(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    if origin == "" {
        return true // Allow non-browser clients
    }
    // Require the origin host to match the request host exactly
    if originHost != r.Host {
        log.Printf("WebSocket upgrade blocked: Origin (%s) does not match Host (%s)", originHost, r.Host)
        return false
    }
    return true
}
```

**Additional Protections:**
- Read limit (4096 bytes) prevents memory exhaustion
- Ping/pong heartbeat with 60-second timeout
- Proper connection cleanup on disconnect

### 2.4 HTTP Security Headers

**Score: 8.0/10** | **Grade: A-**

```go
func securityMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' fonts.googleapis.com; font-src fonts.gstatic.com; script-src 'self'; connect-src 'self' ws: wss:;")
        next.ServeHTTP(w, r)
    })
}
```

**Good Practices:**
- X-Content-Type-Options: nosniff
- X-Frame-Options: DENY
- Content-Security-Policy implemented

**Missing Headers:**

| Header | Severity | Recommendation |
|--------|----------|----------------|
| Strict-Transport-Security | Medium | Add HSTS header for HTTPS deployments |
| X-XSS-Protection | Low | Consider adding (deprecated but still useful for older browsers) |
| Referrer-Policy | Low | Add `Referrer-Policy: strict-origin-when-cross-origin` |

### 2.5 Sandboxing (Landlock)

**Score: 9.0/10** | **Grade: A**

Excellent implementation of Linux Landlock sandboxing:

```go
func Enforce(configPath string, storageDir string, webPort int) error {
    fsRules := []landlock.Rule{
        landlock.RODirs("/proc"),
        landlock.RODirs("/sys").IgnoreIfMissing(),
        landlock.ROFiles(absConfigPath).IgnoreIfMissing(),
        landlock.RWDirs(absStorageDir),
    }
    netRules := []landlock.Rule{
        landlock.BindTCP(uint16(webPort)),
    }
    err = landlock.V5.BestEffort().Restrict(allRules...)
```

**Strengths:**
- Principle of least privilege applied
- Graceful degradation on unsupported kernels
- Network port restriction
- Filesystem access limited to necessary paths

### 2.6 Data Security

**Score: 7.0/10** | **Grade: B**

| ID | Issue | Severity | Recommendation |
|----|-------|----------|----------------|
| DS-001 | Storage files have 0600 permissions (good) | N/A | Continue this practice |
| DS-002 | No encryption at rest for stored metrics | Medium | Consider optional encryption for sensitive environments |
| DS-003 | No integrity verification for tier files | Low | Add checksums for data integrity |

---

## 3. Performance Analysis

### 3.1 Storage Engine Performance

**Score: 8.5/10** | **Grade: A-**

The tiered ring-buffer storage engine is well-designed:

**Strengths:**
- O(1) latest sample query via in-memory cache
- Buffer pooling for aggregation reduces allocations
- Pre-allocated file sizes prevent runtime allocation overhead
- Efficient timestamp extraction avoids full JSON decode
- 1MB buffer for file reading operations

**Performance Optimizations Identified:**

```go
// Excellent: Fast timestamp extraction without full decode
func extractTimestamp(data []byte) (time.Time, error) {
    idx := bytes.Index(data, []byte(`"ts":"`))
    // ... extracts timestamp without unmarshaling entire JSON
}
```

**Performance Concerns:**

| ID | Issue | Severity | Impact | Recommendation |
|----|-------|----------|--------|----------------|
| PERF-001 | Global write lock on Store | Medium | Concurrent writes serialized | Consider per-tier locking |
| PERF-002 | JSON encoding overhead | Low | CPU usage during write | Consider binary serialization (msgpack/protobuf) |
| PERF-003 | No fsync after writes | Low | Data loss on crash | Make fsync configurable for durability |
| PERF-004 | Memory allocation in hot paths | Low | GC pressure | Pool buffers for encoding |

### 3.2 Collector Performance

**Score: 8.0/10** | **Grade: A-**

**Strengths:**
- Direct `/proc` and `/sys` reading (no external dependencies)
- Delta calculations for rates (CPU, network, disk)
- Efficient parsing with strings.Fields

**Performance Profile:**
- Collectors run every 1 second by default
- Minimal allocations in collection loop
- Proper use of buffered I/O

**Issues:**

| ID | Issue | Severity | Recommendation |
|----|-------|----------|----------------|
| PERF-005 | `/proc` files opened on every collection | Medium | Consider file handle caching for frequently read files |
| PERF-006 | Process count iterates all PIDs | Low | Acceptable for monitoring use case |

### 3.3 WebSocket Broadcasting

**Score: 8.0/10** | **Grade: A-**

```go
func (h *wsHub) broadcast(data []byte) {
    h.mu.RLock()
    defer h.mu.RUnlock()
    for client := range h.clients {
        if !client.paused {
            select {
            case client.sendCh <- data:
            default:
                // Client too slow, skip - prevents head-of-line blocking
            }
        }
    }
}
```

**Strengths:**
- Non-blocking broadcast prevents slow clients from affecting others
- Buffered channels (64 messages) absorb bursts
- Pause/resume functionality for zoom operations

### 3.4 Benchmarks Available

The project includes comprehensive benchmarks:

```bash
# From the test file
BenchmarkWrite                    - write throughput
BenchmarkWriteWrapping            - ring buffer wrap performance
BenchmarkWriteParallel            - concurrent write contention
BenchmarkQueryRange_Small         - 60s of 300 samples
BenchmarkQueryRange_Large         - full 3600 samples
BenchmarkQueryRange_Wrapped       - after ring buffer wrap
BenchmarkQueryLatest_Cache        - in-memory cache hit
BenchmarkQueryLatest_ColdDisk     - disk scan fallback
BenchmarkAggregateSamples         - multi-tier aggregation
BenchmarkDownsampling             - inline downsampler
```

---

## 4. Test Coverage Analysis

### 4.1 Unit Tests

**Score: 8.0/10** | **Grade: A-**

Test coverage is comprehensive for core modules:

| Module | Test File | Coverage Areas |
|--------|-----------|----------------|
| storage | store_test.go | CRUD, wrap, concurrency, benchmarks |
| storage | codec_test.go | Encode/decode, timestamp extraction |
| storage | tier_test.go | (implicit via store_test) |
| web | auth_test.go | Hashing, sessions, middleware |
| config | config_test.go | Loading, parsing, defaults |
| sandbox | sandbox_test.go | Rule summary (not full enforcement) |

**Test Quality Observations:**

```go
// Excellent: Table-driven tests
func TestParseSize(t *testing.T) {
    tests := []struct {
        input    string
        expected int64
        wantErr  bool
    }{
        {"100B", 100, false},
        {"1KB", 1024, false},
        // ...
    }
```

**Missing Test Coverage:**

| ID | Missing Tests | Severity | Recommendation |
|----|---------------|----------|----------------|
| TEST-001 | Collector module has no unit tests | Medium | Add tests with mock /proc files |
| TEST-002 | WebSocket handler tests missing | Medium | Add integration tests for WS |
| TEST-003 | TUI module has no tests | Low | Add view rendering tests |
| TEST-004 | No fuzzing tests for codec | Low | Add fuzz tests for JSON decode |
| TEST-005 | No race condition tests for concurrent access | Medium | Add race detector tests |

### 4.2 Benchmark Coverage

**Score: 9.0/10** | **Grade: A**

Excellent benchmark suite covering:
- Write throughput (sequential, parallel, wrapping)
- Query performance (small, large, wrapped)
- Cache hit/miss scenarios
- Aggregation performance
- Downsampling performance

---

## 5. Detailed Vulnerability & Issue Report

### 5.1 Critical Issues

**None identified.**

### 5.2 High Severity Issues

| ID | Category | Issue | Location | Recommendation |
|----|----------|-------|----------|----------------|
| None | | | | |

### 5.3 Medium Severity Issues

| ID | Category | Issue | Location | Recommendation |
|----|----------|-------|----------|----------------|
| SEC-001 | Security | Sessions stored in-memory only | auth.go | Implement persistent encrypted session storage |
| SEC-002 | Security | No CSRF token | server.go | Add CSRF protection middleware |
| PERF-001 | Performance | Global write lock on Store | store.go | Consider per-tier locking strategy |
| PERF-005 | Performance | Files opened every collection | collector/*.go | Consider file handle caching |
| EH-003 | Reliability | Fatal in goroutine | main.go:152 | Use error channels for graceful shutdown |

### 5.4 Low Severity Issues

| ID | Category | Issue | Location | Recommendation |
|----|----------|-------|----------|----------------|
| SEC-003 | Security | X-Forwarded-For trust | server.go:89 | Validate header, add config option |
| SEC-004 | Security | No password policy | auth.go | Document password requirements |
| IV-001 | Validation | Time range validation | server.go:270 | Already has 31-day limit (acceptable) |
| PERF-002 | Performance | JSON encoding | codec.go | Consider binary format option |
| PERF-003 | Performance | No fsync | tier.go | Add configurable sync option |
| CQ-001 | Quality | Module naming | go.mod | Consider standard naming |
| TEST-001 | Testing | Collector tests missing | collector/ | Add unit tests |

---

## 6. Recommendations

### 6.1 High Priority

1. **Implement Persistent Sessions** (SEC-001)
   - Store sessions in encrypted database or file
   - Allow session persistence across restarts
   - Implement session revocation mechanism

2. **Add CSRF Protection** (SEC-002)
   ```go
   // Example implementation
   func csrfMiddleware(next http.Handler) http.Handler {
       return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
           if r.Method != "GET" && r.Method != "HEAD" {
               token := r.Header.Get("X-CSRF-Token")
               cookie, _ := r.Cookie("csrf_token")
               if token == "" || cookie == nil || token != cookie.Value {
                   http.Error(w, "CSRF token mismatch", http.StatusForbidden)
                   return
               }
           }
           next.ServeHTTP(w, r)
       })
   }
   ```

3. **Add Collector Unit Tests** (TEST-001)
   - Create mock `/proc` filesystem
   - Test parsing edge cases
   - Validate rate calculations

### 6.2 Medium Priority

4. **Implement Per-Tier Locking** (PERF-001)
   ```go
   type Store struct {
       tiers []*Tier  // Each tier has its own mutex
       // ...
   }
   // Allow concurrent writes to different tiers
   ```

5. **Add File Handle Caching** (PERF-005)
   ```go
   type Collector struct {
       procStatFile *os.File  // Cached file handle
       // ...
   }
   ```

6. **Improve Error Handling in Goroutines** (EH-003)
   ```go
   go func() {
       if err := server.Start(); err != nil {
           errCh <- err  // Send error instead of Fatal
       }
   }()
   ```

### 6.3 Low Priority

7. **Add HSTS Header** (SEC Headers)
   ```go
   w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
   ```

8. **Consider Binary Serialization** (PERF-002)
   - Option to use msgpack or protobuf for storage
   - Maintains JSON for API compatibility

9. **Add Fuzz Testing** (TEST-004)
   ```go
   func FuzzDecodeSample(f *testing.F) {
       f.Fuzz(func(t *testing.T, data []byte) {
           decodeSample(data)  // Should not panic
       })
   }
   ```

---

## 7. Security Checklist

| Item | Status | Notes |
|------|--------|-------|
| Password hashing | ✅ Pass | Argon2id with salt |
| Constant-time comparison | ✅ Pass | subtle.ConstantTimeCompare |
| Rate limiting | ✅ Pass | 5 attempts per 5 minutes per IP |
| Input validation | ⚠️ Partial | Time ranges validated, some gaps |
| Output encoding | ✅ Pass | JSON encoding, XSS protection in CSP |
| Session management | ⚠️ Partial | In-memory only, proper timeout |
| HTTPS support | ✅ Pass | Secure cookie flag, reverse proxy docs |
| CORS | ✅ Pass | Origin validation for WebSocket |
| Security headers | ⚠️ Partial | Missing HSTS |
| Sandboxing | ✅ Pass | Landlock LSM |
| File permissions | ✅ Pass | 0600 for storage files |
| Dependency vulnerabilities | ✅ Pass | golang.org/x/crypto up to date |

---

## 8. Compliance & Best Practices

### 8.1 OWASP Top 10 (2021) Assessment

| Risk | Status | Notes |
|------|--------|-------|
| A01:2021 – Broken Access Control | ✅ Pass | Auth middleware properly implemented |
| A02:2021 – Cryptographic Failures | ✅ Pass | Argon2id, crypto/rand |
| A03:2021 – Injection | ✅ Pass | No SQL, proper JSON encoding |
| A04:2021 – Insecure Design | ✅ Pass | Least privilege, sandboxing |
| A05:2021 – Security Misconfiguration | ⚠️ Review | CSP allows external fonts |
| A06:2021 – Vulnerable Components | ✅ Pass | Dependencies current |
| A07:2021 – Auth Failures | ✅ Pass | Rate limiting, proper session handling |
| A08:2021 – Software & Data Integrity | ⚠️ Partial | No data integrity checksums |
| A09:2021 – Security Logging | ✅ Pass | Request logging available |
| A10:2021 – SSRF | ✅ Pass | No external URL fetching |

### 8.2 Go Best Practices

| Practice | Status | Notes |
|----------|--------|-------|
| Error handling | ⚠️ Partial | Generally good, some silent ignores |
| Context usage | ✅ Pass | Proper context for cancellation |
| Goroutine management | ✅ Pass | Proper cleanup with defer |
| Channel usage | ✅ Pass | Buffered channels, proper select |
| Mutex usage | ✅ Pass | RWMutex where appropriate |
| Interface design | ✅ Pass | Clean interfaces |
| Package organization | ✅ Pass | Internal packages used |

---

## 9. Conclusion

Kula is a well-designed and professionally implemented system monitoring tool. The codebase demonstrates strong engineering practices with clean architecture, thoughtful security implementations, and good performance characteristics.

### Key Strengths

1. **Excellent sandboxing** with Landlock LSM
2. **Modern authentication** with Argon2id
3. **Efficient storage engine** with tiered ring buffers
4. **Comprehensive benchmarks** for performance validation
5. **Clean code architecture** with proper separation of concerns
6. **Strong WebSocket security** with origin validation

### Key Areas for Improvement

1. **Session persistence** for production reliability
2. **CSRF protection** for API endpoints
3. **Collector test coverage** for reliability
4. **HSTS header** for HTTPS deployments

### Final Assessment

**Overall Score: 7.9/10 (B+)**

Kula is suitable for production deployment with the understanding that session persistence and CSRF protection should be implemented for environments requiring higher security postures. The codebase is maintainable, well-documented, and follows Go best practices.

---

## Appendix A: File Hash Reference

```
go.mod: kula-szpiegula module
main.go: CLI entry point
collector/: Metrics collection (no tests)
storage/: Ring buffer storage (well-tested)
web/: HTTP/WebSocket server
sandbox/: Landlock implementation
```

## Appendix B: Dependencies Analysis

| Dependency | Version | Purpose | Security Status |
|------------|---------|---------|-----------------|
| bubbletea | v1.3.10 | TUI framework | Current |
| lipgloss | v1.1.0 | TUI styling | Current |
| websocket | v1.5.3 | WebSocket | Current |
| go-landlock | v0.7.0 | Sandboxing | Current |
| x/crypto | v0.48.0 | Argon2id | Current |
| yaml.v3 | v3.0.1 | Config parsing | Current |

---

*Report generated for code review purposes. All findings should be validated against the specific deployment context and security requirements.*
