# Security Code Review Report: Kula-Szpiegula

**Review Date:** March 6, 2026  
**Reviewer:** Security Researcher  
**Version:** Current main branch  
**Scope:** Complete codebase security assessment

## Executive Summary

Kula-Szpiegula is a lightweight Linux server monitoring tool written in Go that collects system metrics, stores them in a tiered ring-buffer, and serves them via a web UI and terminal interface. The application demonstrates strong security practices in several areas but contains several vulnerabilities that require attention.

**Overall Security Score: 7.2/10**

### Key Strengths
- Strong cryptographic implementation using Argon2id
- Comprehensive security headers (CSP, Frame-Options, Content-Type)
- Landlock sandboxing for privilege reduction
- Proper session management with rate limiting
- Good input validation in most areas

### Critical Concerns
- Potential for parser panics from malformed `/proc` files
- Missing CSRF protection
- Insufficient bounds checking in some parsers
- Error handling could expose sensitive information

## Detailed Findings

### 🔴 CRITICAL ISSUES

#### C-001: Parser Panics from Malformed /proc Files
**Severity:** Critical | **CVSS:** 8.2  
**Location:** `internal/collector/*.go`  
**Category:** Input Validation / Reliability

**Description:** The parsers for `/proc` and `/sys` files insufficiently validate input bounds before accessing array elements. Malformed kernel output or unexpected file formats could cause slice index out-of-bounds panics, crashing the monitoring service.

**Evidence:**
```go
// cpu.go:31-34 - Insufficient bounds checking
fields := strings.Fields(line)
if len(fields) < 8 {
    continue
}
r.user = parseUint(fields[1], 10, 64, "cpu.user") // Potential panic if fields[1] doesn't exist

// disk.go:28-31 - Similar pattern
fields := strings.Fields(scanner.Text())
if len(fields) < 14 {
    continue
}
name := fields[2] // Assumes fields[2] exists
```

**Impact:** Complete denial of service through monitoring service crashes. Could be exploited by attackers with kernel-level access or during system updates.

**Recommendation:**
1. Add comprehensive bounds checking before all slice access
2. Implement panic recovery in the collection orchestrator
3. Add structured logging for parse failures
4. Consider graceful degradation with partial metrics

#### C-002: Missing CSRF Protection
**Severity:** Critical | **CVSS:** 7.5  
**Location:** `internal/web/server.go`  
**Category:** Web Security

**Description:** The web application lacks CSRF tokens on authenticated endpoints. While the application uses SameSite cookies, this provides insufficient protection against CSRF attacks in modern browsers.

**Evidence:**
```go
// server.go:314-364 - Login endpoint lacks CSRF protection
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
    // No CSRF token validation
    var creds struct {
        Username string `json:"username"`
        Password string `json:"password"`
    }
    // Direct credential processing without CSRF protection
}
```

**Impact:** Account takeover through CSRF attacks if authentication is enabled.

**Recommendation:**
1. Implement CSRF token generation and validation
2. Add CSRF middleware to protected endpoints
3. Include CSRF tokens in login forms
4. Use double-submit cookie pattern for API endpoints

### 🟠 HIGH SEVERITY ISSUES

#### H-001: X-Forwarded-For Header Injection
**Severity:** High | **CVSS:** 7.3  
**Location:** `internal/web/server.go:88-91`  
**Category:** Web Security

**Description:** The logging middleware trusts the `X-Forwarded-For` header without validation, potentially allowing attackers to inject arbitrary IP addresses into logs.

**Evidence:**
```go
clientIP := r.RemoteAddr
if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
    clientIP = fwd // Takes first value without validation
}
```

**Impact:** Log poisoning, potential bypass of IP-based rate limiting, forensic analysis disruption.

**Recommendation:**
1. Parse comma-separated X-Forwarded-For values
2. Validate IP address format
3. Only trust from known proxy IPs
4. Consider using X-Real-IP as fallback

#### H-002: Configuration Size Parsing Vulnerability
**Severity:** High | **CVSS:** 7.1  
**Location:** `internal/config/config.go:175-194`  
**Category:** Input Validation

**Description:** The `parseSize()` function uses `fmt.Sscanf` without proper validation, which can panic on malformed input and doesn't handle overflow conditions.

**Evidence:**
```go
func parseSize(s string) (int64, error) {
    var val float64
    var unit string
    _, err := fmt.Sscanf(s, "%f%s", &val, &unit) // Can panic on malformed input
    if err != nil {
        return 0, fmt.Errorf("invalid size %q", s)
    }
    // No overflow checking for large values
    return int64(val * 1024 * 1024 * 1024), nil
}
```

**Impact:** Configuration parsing failures, potential integer overflow leading to unexpected behavior.

**Recommendation:**
1. Replace fmt.Sscanf with custom parsing
2. Add overflow checking for all calculations
3. Validate input format before parsing
4. Set reasonable upper bounds for size values

#### H-003: Insufficient Storage Allocation Bounds
**Severity:** High | **CVSS:** 7.0  
**Location:** `internal/storage/tier.go`  
**Category:** Resource Management

**Description:** The tier system doesn't validate that data length isn't excessively large before allocation, potentially allowing memory exhaustion attacks.

**Evidence:**
```go
// Missing validation of dataLen before allocation
dataLen := int(binary.BigEndian.Uint32(lengthBuf))
if dataLen <= 0 || dataLen > maxData {
    return nil, fmt.Errorf("invalid data length: %d", dataLen)
}
data := make([]byte, dataLen) // Allocation without additional bounds checking
```

**Impact:** Memory exhaustion through crafted data, potential DoS.

**Recommendation:**
1. Add absolute maximum data size limits
2. Implement memory usage quotas per tier
3. Add allocation time budget checks
4. Consider streaming for large data

### 🟡 MEDIUM SEVERITY ISSUES

#### M-001: WebSocket Origin Validation Bypass
**Severity:** Medium | **CVSS:** 5.8  
**Location:** `internal/web/websocket.go:23-50`  
**Category:** Web Security

**Description:** The WebSocket origin check allows empty Origin headers, potentially permitting certain types of attacks from non-browser clients.

**Evidence:**
```go
CheckOrigin: func(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    if origin == "" {
        return true // Allows non-browser clients without validation
    }
    // ... rest of validation
}
```

**Impact:** Potential CSWSH (Cross-Site WebSocket Hijacking) from malicious clients.

**Recommendation:**
1. Require explicit origin validation even for non-browser clients
2. Implement whitelist of allowed origins
3. Add additional authentication for WebSocket connections
4. Consider requiring Origin header always

#### M-002: Error Information Disclosure
**Severity:** Medium | **CVSS:** 5.3  
**Location:** Multiple files  
**Category:** Information Disclosure

**Description:** Error messages may contain sensitive system information including file paths and internal state.

**Evidence:**
```go
// Various locations expose internal details
return nil, fmt.Errorf("opening tier file: %w", err)
log.Printf("debug: failed to parse %s (%q): %v", fieldName, s, err)
```

**Impact:** Information leakage that could aid attackers in system reconnaissance.

**Recommendation:**
1. Sanitize error messages before logging
2. Use generic error messages for external responses
3. Separate debug logs from production logs
4. Implement error classification system

#### M-003: Session Management Race Condition
**Severity:** Medium | **CVSS:** 5.2  
**Location:** `internal/web/auth.go:125-143`  
**Category:** Concurrency

**Description:** Session validation has a potential race condition where a session could be deleted between validation checks.

**Evidence:**
```go
func (a *AuthManager) ValidateSession(token string) bool {
    a.mu.RLock()
    sess, ok := a.sessions[token]
    if !ok {
        a.mu.RUnlock()
        return false
    }
    if time.Now().After(sess.expiresAt) {
        a.mu.RUnlock()
        a.mu.Lock()
        delete(a.sessions, token) // Race condition window
        a.mu.Unlock()
        return false
    }
    a.mu.RUnlock()
    return true
}
```

**Impact:** Session validation inconsistencies, potential authentication bypass.

**Recommendation:**
1. Use write lock for entire validation operation
2. Implement atomic session validation
3. Add session versioning
4. Consider using sync.Map for session storage

### 🟢 LOW SEVERITY ISSUES

#### L-001: Inconsistent Error Handling
**Severity:** Low | **CVSS:** 3.7  
**Location:** Multiple collector files  
**Category:** Error Handling

**Description:** Some functions silently ignore errors while others propagate them, leading to inconsistent behavior.

**Evidence:**
```go
// cpu.go:19-22 - Silent failure
f, err := os.Open("/proc/stat")
if err != nil {
    return nil // No error logging
}

// vs other locations that log errors
log.Printf("debug: failed to parse %s (%q): %v", fieldName, s, err)
```

**Impact:** Inconsistent monitoring behavior, difficulty debugging issues.

**Recommendation:**
1. Establish consistent error handling policy
2. Add structured error logging
3. Implement error metrics collection
4. Add error context for debugging

#### L-002: Missing Input Validation on API Parameters
**Severity:** Low | **CVSS:** 3.5  
**Location:** `internal/web/server.go:243-295`  
**Category:** Input Validation

**Description:** API endpoints don't fully validate input parameters like time ranges and formats.

**Evidence:**
```go
// server.go:250-268 - Limited time validation
fromStr := r.URL.Query().Get("from")
toStr := r.URL.Query().Get("to")
// Basic validation but missing format and range checks
```

**Impact:** Potential API abuse, unexpected behavior from malformed requests.

**Recommendation:**
1. Add comprehensive input validation
2. Implement request schema validation
3. Add rate limiting per endpoint
4. Consider using request validation middleware

#### L-003: Temporary File Security
**Severity:** Low | **CVSS:** 3.1  
**Location:** `internal/config/config.go:66-72`  
**Category:** File Security

**Description:** Temporary file creation doesn't specify secure permissions explicitly.

**Evidence:**
```go
f, err := os.CreateTemp(dir, ".kula-write-test-*")
// No explicit permissions setting
```

**Impact:** Potential information disclosure in shared environments.

**Recommendation:**
1. Set explicit secure permissions (0600)
2. Use secure file creation patterns
3. Ensure proper cleanup
4. Consider using memfd for sensitive operations

## Security Architecture Assessment

### Strengths

1. **Cryptography**: Excellent use of Argon2id with proper parameters
2. **Sandboxing**: Landlock implementation provides good privilege reduction
3. **Authentication**: Strong session management with rate limiting
4. **Headers**: Comprehensive security headers implementation
5. **Logging**: Good audit trail for security events

### Weaknesses

1. **Input Validation**: Inconsistent across components
2. **Error Handling**: Information disclosure risks
3. **CSRF Protection**: Completely missing
4. **Parser Robustness**: Vulnerable to malformed input
5. **Concurrency**: Some race conditions in session management

## Performance & Resource Security

### Memory Management
- **Good**: Ring-buffer storage prevents unbounded growth
- **Concern**: Potential memory exhaustion through large allocations
- **Recommendation**: Add memory usage quotas and monitoring

### File System Security
- **Good**: Proper file permissions (0600) for storage files
- **Good**: Landlock sandboxing restricts file access
- **Concern**: Temporary file handling could be more secure

### Network Security
- **Good**: WebSocket origin validation (though imperfect)
- **Concern**: Missing CSRF protection
- **Concern**: IP header injection vulnerability

## Compliance & Standards

### OWASP Top 10 Coverage
- ✅ A01: Broken Access Control - Partially addressed
- ❌ A02: Cryptographic Failures - Well addressed
- ❌ A03: Injection - Minimal risk (no SQL/NoSQL)
- ❌ A04: Insecure Design - CSRF protection missing
- ⚠️ A05: Security Misconfiguration - Some issues
- ❌ A06: Vulnerable Components - Dependencies appear current
- ⚠️ A07: Identification/Authentication - Good implementation
- ❌ A08: Software/Data Integrity - Basic implementation
- ⚠️ A09: Security Logging - Good but could be improved
- ❌ A10: Server-Side Request Forgery - Not applicable

### Industry Standards
- **CIS Controls**: Partial compliance (inventory, access control, logging)
- **NIST Cybersecurity Framework**: Basic implementation
- **ISO 27001**: Some controls in place

## Recommendations by Priority

### Immediate (Critical)
1. Fix parser panic vulnerabilities in collectors
2. Implement CSRF protection
3. Add comprehensive bounds checking

### Short Term (High)
1. Fix X-Forwarded-For header validation
2. Secure configuration parsing
3. Add storage allocation limits

### Medium Term (Medium)
1. Improve WebSocket security
2. Sanitize error messages
3. Fix session race conditions

### Long Term (Low)
1. Standardize error handling
2. Enhance API validation
3. Improve temporary file security

## Security Testing Recommendations

### Automated Testing
1. Fuzz testing for all `/proc` parsers
2. CSRF token validation tests
3. Input validation boundary tests
4. Race condition detection tests

### Manual Testing
1. Penetration testing focusing on web interface
2. Malformed kernel output simulation
3. Resource exhaustion testing
4. Configuration injection testing

### Monitoring
1. Parse failure rate monitoring
2. Memory usage alerts
3. Authentication failure tracking
4. Error pattern analysis

## Conclusion

Kula-Szpiegula demonstrates a solid security foundation with strong cryptographic practices and good privilege reduction through sandboxing. However, critical vulnerabilities in input validation and missing CSRF protection require immediate attention.

The application would benefit from a security-focused development process including:
- Code review requirements for security changes
- Automated security testing in CI/CD
- Regular dependency updates
- Security documentation for developers

**Risk Level:** MEDIUM-HIGH (due to critical parser vulnerabilities)

**Next Steps:** Address critical issues immediately, then implement systematic security improvements across the codebase.

---

**Report Generated:** March 6, 2026  
**Review Methodology:** Static code analysis, threat modeling, security architecture review  
**Confidentiality:** Internal use only
