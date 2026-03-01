# Updated Code Review Report: Kula-Szpiegula v0.4.1-beta

**Repository:** https://github.com/c0m4r/kula  
**Project:** Kula-Szpiegula - Lightweight Linux Server Monitoring Tool  
**Version Reviewed:** 0.4.1-beta (updated from 0.3.1)  
**Review Date:** March 2026  

---

## Summary of Changes

The maintainer has addressed **most** of the security issues identified in the previous review. This is an excellent response that demonstrates commitment to security.

---

## ✅ Issues FIXED

### 1. CRITICAL: Mutex Race Condition in `auth.go` ✅ FIXED

**Previous Issue:** Deleting from map while holding RLock  
**Fix:** Proper lock upgrade pattern implemented

```go
// FIXED - Now properly handles lock upgrade
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
        delete(a.sessions, token)
        a.mu.Unlock()
        return false
    }
    a.mu.RUnlock()
    return true
}
```

### 2. HIGH: Custom Whirlpool Cryptography ✅ FIXED

**Previous Issue:** Custom Whirlpool implementation  
**Fix:** Replaced with Argon2id from `golang.org/x/crypto/argon2`

```go
// FIXED - Now using Argon2id
func HashPassword(password, salt string) string {
    timeParam := uint32(1)
    memory := uint32(64 * 1024)  // 64MB
    threads := uint8(4)
    keyLen := uint32(32)

    hash := argon2.IDKey([]byte(password), []byte(salt), 
                         timeParam, memory, threads, keyLen)
    return hex.EncodeToString(hash)
}
```

**Parameters used are reasonable for general use.**

### 3. HIGH: World-Readable Storage Files ✅ FIXED

**Previous Issue:** Files created with `0644` permissions  
**Fix:** Now using `0600` (owner read/write only)

```go
// FIXED - 0644 → 0600
f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
```

### 4. MEDIUM: Missing Security Headers ✅ FIXED

**Previous Issue:** No security headers on HTTP responses  
**Fix:** Added `securityMiddleware` with comprehensive headers

```go
func securityMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Content-Security-Policy", 
            "default-src 'self' 'unsafe-inline'; style-src 'self' fonts.googleapis.com; ...")
        next.ServeHTTP(w, r)
    })
}
```

### 5. MEDIUM: No Time Range Validation ✅ FIXED

**Previous Issue:** Could request extremely large time ranges  
**Fix:** Added validation with 31-day maximum

```go
if to.Sub(from) > 31*24*time.Hour {
    http.Error(w, `{"error":"time range too large, max 31 days allowed"}`, 
               http.StatusBadRequest)
    return
}
if to.Sub(from) < 0 {
    http.Error(w, `{"error":"time range inverted"}`, http.StatusBadRequest)
    return
}
```

### 6. MEDIUM: WebSocket Message Size Unlimited ✅ FIXED

**Previous Issue:** No message size limits on WebSocket  
**Fix:** Added `MaxPayloadBytes` limit

```go
conn.MaxPayloadBytes = 4096 // Limit message size to prevent memory exhaustion
```

### 7. MEDIUM: Cookie Without Secure Flag ✅ FIXED

**Previous Issue:** Session cookie didn't have Secure flag  
**Fix:** Now sets Secure flag when using HTTPS or behind reverse proxy

```go
http.SetCookie(w, &http.Cookie{
    Name:     "kula_session",
    Value:    token,
    Path:     "/",
    HttpOnly: true,
    Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
    SameSite: http.SameSiteStrictMode,
})
```

### 8. LOW: Panic on Token Generation Error ✅ FIXED

**Previous Issue:** `generateToken()` panicked on `crypto/rand` error  
**Fix:** Now returns error instead of panicking

```go
func generateToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
    }
    return hex.EncodeToString(b), nil
}
```

---

## 🆕 NEW SECURITY FEATURES ADDED

### 1. Landlock Sandbox (`internal/sandbox/sandbox.go`)

Excellent addition! Implements Linux Landlock LSM for process sandboxing:

```go
// Enforce Landlock sandbox: restrict filesystem and network access
if err := sandbox.Enforce(configPath, cfg.Storage.Directory, cfg.Web.Port); err != nil {
    log.Printf("Warning: Landlock sandbox not enforced: %v", err)
}
```

**Sandbox Restrictions:**
- `/proc` and `/sys`: read-only (for metrics collection)
- Config file: read-only
- Storage directory: read-write
- Network: bind only on configured TCP port
- Graceful degradation on unsupported kernels

### 2. API Access & Performance Logging

New configurable logging middleware:

```go
type LogConfig struct {
    Enabled bool   `yaml:"enabled"`
    Level   string `yaml:"level"` // "access" or "perf"
}
```

Logs include:
- Client IP (with X-Forwarded-For support)
- HTTP method and path
- Response status code
- Request duration
- Database fetch metrics (in perf mode)

### 3. Password Input Masking

Password input now shows asterisks instead of being invisible:

```go
func readPasswordWithAsterisks() string {
    // ... terminal handling ...
    password = append(password, b[0])
    fmt.Print("*")  // Visual feedback
}
```

---

## ⚠️ REMAINING ISSUES

### 1. MEDIUM: No Rate Limiting on Login (Still Unaddressed)

The `/api/login` endpoint still has no rate limiting, making it vulnerable to brute-force attacks.

**Recommendation:** Implement a simple in-memory rate limiter:

```go
type RateLimiter struct {
    mu      sync.Mutex
    attempts map[string][]time.Time // IP -> timestamps
}

func (rl *RateLimiter) Allow(ip string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    now := time.Now()
    cutoff := now.Add(-5 * time.Minute)

    // Filter old attempts
    var recent []time.Time
    for _, t := range rl.attempts[ip] {
        if t.After(cutoff) {
            recent = append(recent, t)
        }
    }

    if len(recent) >= 5 { // Max 5 attempts per 5 minutes
        return false
    }

    rl.attempts[ip] = append(recent, now)
    return true
}
```

### 2. LOW: Directory Traversal Risk (Still Present)

```go
path := filepath.Join(cfg.Directory, fmt.Sprintf("tier_%d.dat", i))
```

While `filepath.Join` helps, there's no validation that `cfg.Directory` doesn't contain traversal sequences like `../../../etc/`.

**Recommendation:** Add path validation:

```go
func validateStoragePath(path string) error {
    abs, err := filepath.Abs(path)
    if err != nil {
        return err
    }
    // Ensure path doesn't escape intended directory
    // (implementation depends on requirements)
    return nil
}
```

### 3. LOW: Silent Error Handling in Collectors (Still Present)

```go
// internal/collector/network.go
n.rxBytes, _ = strconv.ParseUint(fields[0], 10, 64)  // Still silent
```

Parsing errors are still silently ignored. While this is less critical for monitoring data, it could mask issues.

**Recommendation:** At minimum, log parsing errors at debug level:

```go
if val, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
    n.rxBytes = val
} else {
    log.Printf("debug: failed to parse rxBytes: %v", err)
}
```

### 4. LOW: No Context Cancellation (Still Present)

The codebase still doesn't use `context.Context` for graceful shutdown.

### 5. LOW: JSON Storage Format (Still Present)

JSON is still used for storage. While functional, it has:
- No schema versioning
- Larger storage footprint than binary formats
- Slower than binary encoding

**Note:** The `extractTimestamp` optimization helps mitigate this:

```go
// Fast timestamp pre-filtering without full decode
func extractTimestamp(data []byte) (time.Time, error) {
    idx := bytes.Index(data, []byte(`"ts":"`))
    // ... parse timestamp directly from JSON
}
```

---

## 📊 UPDATED RATINGS

| Category | Previous | Current | Change |
|----------|----------|---------|--------|
| **Security** | 5/10 | **8/10** | +3 ✅ |
| Architecture | 8/10 | 9/10 | +1 |
| Code Quality | 7/10 | 8/10 | +1 |
| Performance | 7/10 | 8/10 | +1 |
| Testing | 5/10 | 5/10 | - |

**Overall Rating: 7.6/10 → 8.4/10** ⭐

---

## 🎯 RECOMMENDATIONS (Updated Priority)

### Medium Priority
1. **Add rate limiting to login endpoint** - Prevents brute-force attacks
2. **Add path validation for storage directory** - Prevents directory traversal

### Low Priority
3. Add debug logging for collector parsing errors
4. Implement context-based graceful shutdown
5. Consider binary encoding (MessagePack) for storage
6. Increase test coverage

---

## 🏆 CONCLUSION

The maintainer has done an **excellent job** addressing the security issues. The changes demonstrate:

1. **Quick response** to security concerns
2. **Proper implementation** of fixes (Argon2, Landlock, proper locking)
3. **Additional hardening** beyond what was requested (sandbox, logging, buffered I/O)
4. **Graceful degradation** for unsupported features

The codebase is now **significantly more secure** and suitable for production deployment after addressing the remaining medium-priority issues (rate limiting, path validation).

**Special kudos for:**
- Implementing Landlock sandbox (goes above and beyond)
- Proper Argon2id parameters
- Buffered I/O performance improvements
- Security policy and community standards

---

*Report generated by Kimi K2.5 Agent - Security Expert & Code Reviewer*
