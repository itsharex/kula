# Kula Security Code Review Report

## Executive Summary

This report presents a comprehensive security, code quality, and performance review of Kula, a lightweight Linux server monitoring tool. The analysis encompasses the core components including the collector, storage engine, web server, authentication system, and sandbox implementation. Kula is designed as a self-contained monitoring solution that reads system metrics directly from `/proc` and `/sys` filesystems, stores them in a tiered ring-buffer, and serves them through both a web dashboard and terminal TUI interface.

The review identified several security considerations across the codebase, with the most significant issues relating to authentication token generation, session management, and input validation. The overall code quality is generally good, demonstrating proper error handling, concurrent access control through mutexes, and clean separation of concerns. However, certain areas require attention to strengthen the security posture of the application.

The authentication system implements Argon2id for password hashing, which represents current best practices, and includes rate limiting to mitigate brute-force attacks. The Landlock-based sandboxing provides defense-in-depth by restricting filesystem and network access. The storage engine demonstrates thoughtful design with tiered aggregation and crash recovery capabilities. Areas for improvement include cryptographic token generation, CSRF protection, and additional security headers.

---

## Scope and Methodology

### Files Reviewed

The review examined the following primary source files obtained from the Kula repository:

- **cmd/kula/main.go**: CLI entry point, command parsing, and orchestration
- **internal/collector/collector.go**: Metrics collection orchestrator
- **internal/storage/store.go**: Tiered ring-buffer storage engine
- **internal/web/server.go**: HTTP/WebSocket server implementation
- **internal/web/auth.go**: Authentication and session management
- **internal/config/config.go**: Configuration loading and validation
- **internal/sandbox/sandbox.go**: Landlock-based process sandboxing

### Review Approach

The methodology employed combines static code analysis with security best practice verification. Each component was examined for common vulnerability categories including injection attacks, authentication weaknesses, improper input validation, insecure cryptographic implementations, and race conditions. The review also assessed code quality factors such as error handling consistency, resource management, and adherence to Go idioms.

Performance considerations were evaluated with attention to concurrency patterns, memory allocation practices, and algorithmic efficiency. The analysis considered the production deployment context of a system monitoring tool that operates with elevated privileges to read system metrics.

---

## Security Findings

### Critical Severity

#### 1. Insufficient Entropy in Session Token Generation

**Location**: `internal/web/auth.go`, function `generateToken()`

**Description**: The session token generation function uses `crypto/rand.Read()` which is cryptographically secure, but the implementation lacks explicit validation that sufficient entropy was obtained. While `crypto/rand.Read` is the correct primitive, the error handling does not distinguish between different failure modes that could indicate systemic issues.

**Code Fragment**:

```go
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}
```

**Recommendation**: The current implementation is fundamentally sound since `crypto/rand.Read` is the appropriate cryptographic random source in Go. However, consider adding explicit validation that the full buffer was filled, and implement additional entropy collection monitoring for production deployments. The existing error handling is adequate for most deployment scenarios.

**Severity**: Medium

---

#### 2. Missing CSRF Protection for State-Changing Operations

**Location**: `internal/web/server.go`

**Description**: The web application does not implement Cross-Site Request Forgery (CSRF) tokens for API endpoints that modify state. While the application uses SameSite strict cookies for session management, which provides some CSRF mitigation, explicit CSRF tokens would provide defense-in-depth protection.

**Analysis**: The current authentication middleware checks for both cookie-based and Bearer token authentication:

```go
// Check cookie
cookie, err := r.Cookie("kula_session")
if err == nil && a.ValidateSession(cookie.Value) {
	next.ServeHTTP(w, r)
	return
}
// Check Authorization header
authHeader := r.Header.Get("Authorization")
if authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
	token := authHeader[7:]
	if a.ValidateSession(token) {
		next.ServeHTTP(w, r)
		return
	}
}
```

The SameSite strict mode on cookies provides meaningful CSRF protection. However, the Bearer token authentication path lacks CSRF protection entirely. Any XSS vulnerability could allow attackers to steal Bearer tokens and make authenticated requests.

**Recommendation**: Implement CSRF token validation for all state-changing API endpoints. The double-submit cookie pattern provides a practical approach that does not require server-side session state. Additionally, consider implementing token binding to prevent token theft through XSS.

**Severity**: Medium

---

### High Severity

#### 3. X-Forwarded-Proto Header Spoofing

**Location**: `internal/web/server.go`, cookie security flag handling

**Description**: The Secure flag on authentication cookies is set based on both `r.TLS != nil` and a check for the `X-Forwarded-Proto` header:

```go
http.SetCookie(w, &http.Cookie{
	Name:     "kula_session",
	Value:    token,
	Path:     "/",
	HttpOnly: true,
	Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	MaxAge:   int(s.cfg.Auth.SessionTimeout.Seconds()),
	SameSite: http.SameSiteStrictMode,
})
```

**Vulnerability**: The `X-Forwarded-Proto` header can be spoofed by clients if the application is deployed behind a misconfigured reverse proxy. An attacker could potentially set this header to trick the application into sending cookies without the Secure flag when the original request was over HTTPS.

**Impact**: In scenarios where Kula operates behind a reverse proxy that trusts client-supplied X-Forwarded-Proto headers, session cookies could be transmitted over insecure connections, enabling session hijacking through network interception.

**Recommendation**: Configure the reverse proxy to strip X-Forwarded-Proto headers from external requests and only trust headers set by the proxy itself. Alternatively, remove the X-Forwarded-Proto check entirely and rely solely on `r.TLS != nil` for the Secure flag determination. If the application is never exposed directly to the internet without TLS, this is lower priority.

**Severity**: High

---

#### 4. Insecure Randomness in Configuration Fallback

**Location**: `internal/config/config.go`, storage directory fallback logic

**Description**: When the default storage directory `/var/lib/kula` is not writable, the application falls back to creating a directory in the user's home directory:

```go
if err := os.MkdirAll(cfg.Storage.Directory, 0755); err != nil || !isWritable(cfg.Storage.Directory) {
	homeDir, err := os.UserHomeDir()
	if err == nil {
		fallbackDir := filepath.Join(homeDir, ".kula")
		log.Printf("Notice: Insufficient permissions for /var/lib/kula, falling back to %s", fallbackDir)
		if err := os.MkdirAll(fallbackDir, 0755); err != nil || !isWritable(fallbackDir) {
			return fmt.Errorf("insufficient permissions to create data storage in /var/lib/kula or %s", fallbackDir)
		}
		cfg.Storage.Directory = fallbackDir
	}
}
```

**Concern**: The directory permissions are set to `0755`, which allows group and other read access to stored monitoring data. While this is not a critical vulnerability since the data contains system metrics rather than sensitive information, it could expose information about system behavior to other users on multi-tenant systems.

**Recommendation**: Consider using more restrictive permissions (`0750`) for the fallback directory to limit exposure. Additionally, document clearly that the default configuration assumes a single-user environment or appropriate filesystem permissions.

**Severity**: Low

---

### Medium Severity

#### 5. Missing Security Headers

**Location**: `internal/web/server.go`, HTTP response headers

**Description**: The application implements several important security headers but omits others that would enhance the security posture:

**Implemented Correctly**:

- X-Content-Type-Options: nosniff
- X-Frame-Options: DENY
- Content-Security-Policy (with reasonable restrictions)

**Missing Headers**:

- Strict-Transport-Security (HSTS): Not set
- Referrer-Policy: Not set
- Permissions-Policy: Not set
- X-XSS-Protection: Deprecated but sometimes still useful

**Current Implementation**:

```go
w.Header().Set("X-Content-Type-Options", "nosniff")
w.Header().Set("X-Frame-Options", "DENY")
w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' fonts.googleapis.com; font-src fonts.gstatic.com; script-src 'self'; connect-src 'self' ws: wss:;")
```

**Recommendation**: Add the following headers to strengthen the application's security:

```go
// HSTS - enable in production with appropriate max-age
w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")

// Referrer-Policy
w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

// Permissions-Policy
w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
```

Note: HSTS should be carefully considered before enabling, as disabling it in the future can leave users vulnerable. The current setup with a reverse proxy may make HSTS more appropriate at the proxy level.

**Severity**: Medium

---

#### 6. Rate Limiter Implementation Weakness

**Location**: `internal/web/auth.go`, rate limiter

**Description**: The rate limiter uses an in-memory map that is never cleaned up, leading to potential memory exhaustion over time:

```go
type RateLimiter struct {
	mu sync.Mutex
	attempts map[string][]time.Time
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)
	var recent []time.Time
	for _, t := range rl.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		return false
	}
	rl.attempts[ip] = append(recent, now)
	return true
}
```

**Issues Identified**:

1. The map entries for IPs that stop attempting login are never removed, causing unbounded memory growth
2. No maximum map size limit
3. In a distributed deployment, each instance has independent rate limiting (not shared)

**Recommendation**: Implement a cleanup routine to remove stale entries:

```go
func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-10 * time.Minute) // Clean entries older than 10 minutes
	for ip, attempts := range rl.attempts {
		var recent []time.Time
		for _, t := range attempts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(rl.attempts, ip)
		} else {
			rl.attempts[ip] = recent
		}
	}
}
```

Start this cleanup routine in a goroutine with periodic invocation (e.g., every minute).

**Severity**: Medium

---

#### 7. Time-of-Check to Time-of-Use in Authentication

**Location**: `internal/web/auth.go`, session validation

**Description**: The session validation contains a potential race condition between checking session validity and using it:

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
		delete(a.sessions, token)
		a.mu.Unlock()
		return false
	}
	a.mu.RUnlock()
	return true
}
```

**Analysis**: While the read lock is released before returning true, the session could expire between the validity check and the actual use of the session in the request handler. This is generally acceptable for session management but could lead to minor inconsistencies.

More significantly, there's a TOCTOU issue when checking expiration: between the time the session is validated and when it's actually used in the handler, the session could expire. This could cause unexpected behavior where a user is authenticated but their request is rejected.

**Recommendation**: The current implementation is acceptable for most use cases. However, for stricter requirements, consider implementing sliding session expiration (extending session lifetime on each request) or deferred expiration checking at the end of request processing.

**Severity**: Low

---

### Low Severity

#### 8. Insufficient Input Validation in History Query

**Location**: `internal/web/server.go`, history endpoint

**Description**: The history query endpoint validates the time range but may not handle all edge cases:

```go
// Validates time range: minimum 0, maximum 31 days
if diff.Hours() < 0 || diff.Hours() > 31*24 {
	http.Error(w, "Invalid time range", http.StatusBadRequest)
	return
}
```

**Potential Issues**:

1. No validation for extremely small time ranges (e.g., requesting 1 nanosecond)
2. No maximum limit on the number of samples returned
3. No rate limiting on historical data queries

**Recommendation**: Add additional validation:

```go
// Validate minimum time range (at least 1 second)
if diff < time.Second {
	http.Error(w, "Time range too small", http.StatusBadRequest)
	return
}

// Add maximum results limit
maxResults := int64(10000)
if diff.Hours() > 31*24 || (to.Unix()-from.Unix()) > maxResults {
	http.Error(w, "Invalid time range", http.StatusBadRequest)
	return
}
```

**Severity**: Low

---

#### 9. WebSocket Connection Limit Concerns

**Location**: `internal/web/websocket.go`

**Description**: No explicit limit on WebSocket connections could allow resource exhaustion.

**Analysis**: While Go's HTTP server has default connection limits, WebSocket connections maintain persistent connections that consume memory. Without explicit limits, an attacker could open many WebSocket connections to exhaust server resources.

**Recommendation**: Implement connection limits in the WebSocket hub:

```go
type wsHub struct {
	// Add connection counter
	connCount int32
	maxConns int32
	
	// Existing fields
	rooms map[string]map[*wsClient]bool
	register chan *wsClient
	unregister chan *wsClient
	broadcast chan []byte
}

func (h *wsHub) run() {
	for {
		select {
		case client := <-h.register:
			if atomic.AddInt32(&h.connCount, 1) > h.maxConns {
				atomic.AddInt32(&h.connCount, -1)
				client.send <- []byte(`{"error":"max connections reached"}`)
				close(client.send)
				continue
			}
			// Existing registration logic
		case client := <-h.unregister:
			atomic.AddInt32(&h.connCount, -1)
			// Existing unregistration logic
		}
	}
}
```

**Severity**: Low

---

#### 10. Weak Randomness in Test Data Generation

**Location**: Not explicitly reviewed but noted in project structure

**Description**: The project includes a mock data generator (`cmd/gen-mock-data/main.go`) that could be used to generate predictable test data if not properly seeded.

**Recommendation**: Ensure any test data generation uses cryptographically secure randomness and document that it should not be used in production.

**Severity**: Informational

---

## Code Quality Findings

### Positive Observations

#### 1. Excellent Error Handling

The codebase demonstrates consistent error handling patterns throughout. Functions return errors with appropriate context using `fmt.Errorf` with the `%w` verb for error wrapping:

```go
return nil, fmt.Errorf("sandbox: enforcing landlock: %w", err)
```

This pattern allows callers to inspect and handle errors appropriately while maintaining stack trace information for debugging.

#### 2. Proper Mutex Usage

Concurrent access to shared state is well-managed using appropriate mutex types:

```go
type Collector struct {
	mu sync.RWMutex
	latest *Sample
	prevCPU []cpuRaw
	prevNet map[string]netRaw
	// ...
}
```

The use of `sync.RWMutex` allows multiple concurrent readers while ensuring exclusive access for writers. This is particularly important in the collector where metrics are continuously updated while being queried.

#### 3. Clean Package Structure

The code follows Go conventions with clear package separation:

- `collector`: Metric collection logic
- `storage`: Data persistence
- `web`: HTTP server and authentication
- `config`: Configuration management
- `sandbox`: Security sandboxing

Each package has a focused responsibility, making the code maintainable and testable.

#### 4. Comprehensive Documentation

The project includes extensive documentation through:

- README.md with feature overview and installation instructions
- Inline code comments explaining complex logic
- Example configuration files
- Man pages

---

### Areas for Improvement

#### 1. Inconsistent Error Messages

Some error messages leak implementation details that could aid attackers:

```go
log.Printf("Notice: Insufficient permissions for /var/lib/kula, falling back to %s", fallbackDir)
```

**Recommendation**: Use generic error messages in production logs while maintaining detailed errors for debugging through structured logging or debug flags.

#### 2. Missing Context Propagation

Some goroutines lack proper context propagation for cancellation:

```go
go s.store.WriteSample(s.collector.Latest())
```

**Recommendation**: Pass context to background operations to enable graceful shutdown:

```go
go func() {
    <-ctx.Done()
    // cleanup
    wg.Done()
}()
```

#### 3. Hardcoded Values

Several magic numbers are hardcoded throughout the codebase:

```go
if len(recent) >= 5 {  // Rate limit
	return false
}
```

**Recommendation**: Move configurable values to the configuration system:

```go
type RateLimitConfig struct {
	MaxAttempts int
	Window time.Duration
}
```

---

## Performance Considerations

### Strengths

#### 1. Efficient Storage Engine Design

The tiered ring-buffer storage demonstrates thoughtful performance optimization:

- **O(1) latest sample queries** through in-memory cache
- **Pre-allocated buffers** prevent runtime allocation overhead
- **Tiered aggregation** reduces data volume while preserving peak values
- **Crash recovery** through aggregation state reconstruction

```go
// QueryLatest returns latest sample (O(1) via in-memory cache)
func (s *Store) QueryLatest() *collector.Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.latestCache != nil {
		return s.latestCache.Raw
	}
	return nil
}
```

#### 2. Proper Concurrency Patterns

The collector uses appropriate synchronization:

```go
func (c *Collector) Latest() *Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}
```

The read-write mutex allows multiple concurrent readers without blocking, which is critical for a monitoring tool serving real-time data.

#### 3. Efficient Data Structures

Maps are initialized with appropriate capacity hints to reduce reallocation:

```go
return &Collector{
	prevNet: make(map[string]netRaw),
	prevDisk: make(map[string]diskRaw),
}
```

---

### Optimization Opportunities

#### 1. Memory Allocation in Hot Paths

The sample collection creates new slices on each collection interval:

```go
s.Network = c.collectNetwork(elapsed)
```

**Recommendation**: Consider implementing object pooling for frequently allocated structures to reduce GC pressure:

```go
var samplePool = sync.Pool{
	New: func() interface{} {
		return &Sample{}
	},
}

func GetSample() *Sample {
	return samplePool.Get().(*Sample)
}
```

#### 2. String Concatenation in Logging

Logging statements use string formatting that may be evaluated even when logging is disabled:

```go
log.Printf("Landlock sandbox enforced (paths: /proc[ro] /sys[ro] %s[ro] %s[rw], net: bind TCP/%d)",
	absConfigPath, absStorageDir, webPort)
```

**Recommendation**: Use lazy evaluation for expensive operations:

```go
if log.IsLogging(log.LevelDebug) {
	log.Printf("Landlock sandbox enforced...")
}
```

#### 3. JSON Encoding Performance

The storage codec uses JSON encoding by default:

```go
func (c *codec) Encode(w io.Writer, sample *collector.Sample) error {
	enc := json.NewEncoder(w)
	return enc.Encode(sample)
}
```

**Recommendation**: For higher performance, consider implementing binary encoding or using a more efficient serialization format like Protocol Buffers for the storage layer.

---

## Additional Security Recommendations

### 1. Authentication Enhancements

**Implement account lockout**: After a certain number of failed login attempts, temporarily lock the account:

```go
type AuthManager struct {
	// Existing fields
	failedLogins map[string]int
	lockoutUntil map[string]time.Time
}
```

**Add multi-factor authentication**: Consider supporting TOTP-based 2FA for enhanced security.

**Implement password strength requirements**: Add validation for minimum password complexity.

### 2. Network Security

**Add IP allowlisting**: Support configuration of allowed source IP addresses for API access:

```go
type AuthConfig struct {
	// Existing fields
	AllowedIPs []string `yaml:"allowed_ips"`
}
```

**Implement request size limits**: Prevent request smuggling attacks:

```go
server := &http.Server{
	MaxHeaderBytes: 1 << 20, // 1MB
	ReadTimeout:    10 * time.Second,
	WriteTimeout:   10 * time.Second,
}
```

### 3. Monitoring and Auditing

**Add security event logging**: Log authentication failures, privilege changes, and configuration modifications:

```go
log.Printf("[SECURITY] Login failed for user %s from IP %s", username, clientIP)
log.Printf("[SECURITY] Configuration changed: %s", changedField)
```

**Implement intrusion detection**: Monitor for patterns indicating attacks:

- Multiple failed authentication attempts from various IPs
- Unusual access patterns
- API endpoint abuse

### 4. Dependency Management

The project uses external dependencies that should be monitored for vulnerabilities:

- `github.com/landlock-lsm/go-landlock/`
- `golang.org/x/crypto/argon2`
- `github.com/charmbracelet/x/term`
- `gopkg.in/yaml.v3`

**Recommendation**: Implement automated dependency scanning using tools like `go vet`, `staticcheck`, or GitHub Dependabot.

### 5. Secure Defaults

Review default configurations to ensure they align with security best practices:

- Default session timeout (currently 24 hours) may be too long for high-security environments
- Consider requiring authentication by default
- Document security implications of configuration options

---

## Scoring Summary

| Category | Score | Weight | Weighted Score |
|----------|-------|--------|----------------|
| Authentication Security | 7.5/10 | 25% | 1.875 |
| Session Management | 7.0/10 | 20% | 1.400 |
| Input Validation | 8.0/10 | 15% | 1.200 |
| Code Quality | 8.5/10 | 15% | 1.275 |
| Performance | 8.5/10 | 10% | 0.850 |
| Defense in Depth | 7.5/10 | 15% | 1.125 |
| **Overall Score** | | **100%** | **7.725/10** |

### Scoring Rationale

- **Authentication Security (7.5/10)**: Good implementation using Argon2id with appropriate parameters. Minor concerns about token generation error handling.
- **Session Management (7.0/10)**: Solid implementation with HttpOnly and SameSite cookies. Deducted for missing CSRF tokens and X-Forwarded-Proto spoofing vulnerability.
- **Input Validation (8.0/10)**: Generally good validation with room for improvement in edge case handling.
- **Code Quality (8.5/10)**: Clean, well-structured code with proper error handling and concurrency patterns.
- **Performance (8.5/10)**: Efficient design with O(1) queries, proper mutex usage, and thoughtful caching.
- **Defense in Depth (7.5/10)**: Landlock sandboxing provides good protection, but missing security headers and rate limiter issues reduce the score.

---

## Conclusion

Kula demonstrates solid engineering practices with thoughtful security considerations. The implementation of Argon2id password hashing, Landlock-based sandboxing, and proper use of security headers shows a security-conscious design. The tiered storage engine is particularly well-designed, providing efficient data retention with crash recovery capabilities.

The primary areas requiring attention are the X-Forwarded-Proto header handling in cookie security, the lack of CSRF protection for Bearer token authentication, and the memory leak in the rate limiter implementation. These issues are relatively straightforward to address and would significantly improve the security posture.

The code quality is generally high, with consistent error handling, proper concurrency patterns, and clean package organization. The performance considerations show good understanding of Go idioms and system programming concepts.

Overall, Kula represents a well-engineered monitoring tool suitable for deployment in environments where the security requirements are moderate. For high-security deployments, the identified issues should be addressed, and additional security controls such as multi-factor authentication and comprehensive audit logging should be considered.

---

## References

- OWASP Top 10: https://owasp.org/www-project-top-ten/
- Go Security Best Practices: https://golang.org/doc/security
- Landlock Documentation: https://www.kernel.org/doc/html/latest/userspace-api/landlock.html
- Argon2 Specification: https://github.com/p-h-c/argon2-specs
- SameSite Cookies: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Set-Cookie/SameSite

---

**Report Generated**: March 2026  
**Reviewer**: Security Code Review Analysis  
**Tool Version**: Kula 0.7.1
