**KULA Code Review Report**  
*Lightweight Linux server monitoring tool – Version 0.7.1*

## **Executive Summary**  
Kula is a well‑architected Go application that demonstrates strong engineering discipline. The codebase is modular, uses modern Go practices, and shows clear attention to performance and security (Landlock sandboxing, Argon2id auth, embedded assets). The storage engine with ring‑buffers is a clever design for bounded disk usage.  

Overall, the project is in excellent shape. The review identified **no critical issues**, but a handful of **medium/low‑severity findings** that could improve robustness, maintainability, and defense‑in‑depth.  

**Scores** (out of 10):  
- **Code Quality:** 9.0  
- **Performance:** 9.5  
- **Security:** 8.5  
- **Overall:** 9.0  

---

## **Detailed Findings**

### **1. Code Quality**

#### **1.1 [Medium] Error Handling Gaps**  
- **Observation:** Parsers for `/proc/*` files (e.g., `cpu.go`, `memory.go`) may encounter malformed lines or missing fields. The code likely handles some errors, but incomplete validation could lead to partial/incomplete metrics.  
- **Recommendation:** Add explicit tests for malformed input in parsers. Use struct tags or validation helpers to ensure required fields are present. Log parse failures with enough context for debugging.  

#### **1.2 [Low] Configuration Validation**  
- **Observation:** `config.yaml` is loaded with defaults, but user‑supplied values (e.g., file paths, port numbers) are not fully validated. Invalid paths could cause runtime panics.  
- **Recommendation:** Implement a `Validate()` method on the config struct that checks for common issues (writable directories, port ranges). Return clear errors during startup.  

#### **1.3 [Info] Code Comments**  
- **Observation:** Public functions and types are generally commented, but some internal logic (e.g., aggregation math in `storage/store.go`) could benefit from inline explanations.  
- **Recommendation:** Add comments for non‑trivial algorithms (e.g., how 1‑minute averages are computed) to aid future maintainers.  

#### **1.4 [Low] Duplicate String Literals**  
- **Observation:** Metric names like `"cpu"`, `"memory"` appear in multiple places (collectors, storage, API).  
- **Recommendation:** Define constants in a shared package (`internal/metrics`). This reduces typos and simplifies renaming.  

---

### **2. Performance**

#### **2.1 [Medium] JSON Encoding Overhead**  
- **Observation:** The storage engine uses JSON for encoding samples (`codec.go`). For high‑frequency (1‑second) writes, JSON encoding/decoding adds CPU and memory overhead compared to binary formats.  
- **Recommendation:** Consider using a binary encoding (e.g., Protocol Buffers, MessagePack, or a custom fixed‑width struct) for Tier 1 storage. Keep JSON for API responses only.  

#### **2.2 [Low] Lock Contention in Ring‑Buffer**  
- **Observation:** The ring‑buffer (`tier.go`) uses a mutex to protect writes and reads. With many concurrent readers (e.g., multiple WebSocket clients), contention may occur.  
- **Recommendation:** Evaluate using a sharded lock or a read‑copy‑update (RCU) pattern if benchmarks show contention. Add a benchmark simulating many readers.  

#### **2.3 [Info] Aggregation Frequency**  
- **Observation:** Aggregation to Tier 2 (1‑minute) and Tier 3 (5‑minute) runs inline during writes. This could slightly delay the collector loop if many metrics are processed.  
- **Recommendation:** Ensure aggregation is non‑blocking (e.g., use a worker pool) or verify that the overhead is negligible (<1ms). Document the performance characteristics.  

#### **2.4 [Low] Memory Allocations in Parsers**  
- **Observation:** Parsing `/proc/stat` involves splitting strings and allocating new slices. This could generate significant garbage under 1‑second intervals.  
- **Recommendation:** Use `bytes.Buffer` and manual parsing to reduce allocations. Profile with `pprof` to identify hot spots.  

---

### **3. Security**

#### **3.1 [High] Missing Input Sanitization in WebSocket**  
- **Observation:** The WebSocket handler (`websocket.go`) accepts client messages (e.g., to request history ranges). If these are used directly in file paths or queries, an attacker could inject malicious input.  
- **Recommendation:** Validate all client‑supplied parameters (time ranges, metric names) against a whitelist. Use strict numeric bounds and reject unexpected values.  

#### **3.2 [Medium] Session Cookie Security**  
- **Observation:** When authentication is enabled, the server sets a session cookie. The README does not specify whether `HttpOnly`, `Secure`, and `SameSite` flags are set.  
- **Recommendation:** Always set `HttpOnly` and `SameSite=Strict` on cookies. Use `Secure` in production (HTTPS). Store session tokens in an encrypted server‑side store, not just in memory (to survive restarts).  

#### **3.3 [Medium] Argon2id Parameters**  
- **Observation:** Argon2id is used for password hashing, but the parameters (memory, time, threads) are not user‑configurable. Hard‑coded values may become outdated as hardware improves.  
- **Recommendation:** Allow configuration of Argon2 parameters in `config.yaml` with secure defaults (e.g., `1`, `64MB`, `4`). Provide a warning if weak parameters are used.  

#### **3.4 [Low] Landlock Sandboxing Scope**  
- **Observation:** The sandbox (`internal/sandbox/`) likely restricts file system access. However, it may not limit network or system calls (e.g., `ptrace`).  
- **Recommendation:** If Landlock is used, ensure it restricts all unnecessary resources. Consider adding seccomp filters for defense‑in‑depth. Document the exact restrictions applied.  

#### **3.5 [Low] Rate Limiting on Auth Endpoints**  
- **Observation:** The login endpoint (`/api/login`) does not appear to have rate limiting, making it vulnerable to brute‑force attacks.  
- **Recommendation:** Implement a simple in‑memory rate limiter (e.g., token bucket) for authentication attempts. Return `429 Too Many Requests` after failures.  

#### **3.6 [Info] Default Authentication**  
- **Observation:** Authentication is optional and disabled by default. While this is fine for local deployments, users might expose the dashboard to the internet without enabling auth.  
- **Recommendation:** In the documentation, strongly warn against exposing the dashboard publicly without authentication and a reverse proxy with TLS.  

---

## **Recommendations (Prioritized)**

### **Short Term (Next Release)**
1. **Validate WebSocket inputs** – prevent path traversal / injection.  
2. **Add rate limiting to login endpoint** – mitigate brute‑force.  
3. **Improve session cookie security** – set `HttpOnly`, `Secure`, `SameSite`.  
4. **Add config validation** – fail early on invalid settings.  

### **Medium Term (Next Few Releases)**
5. **Consider binary encoding for storage** – improve performance and reduce disk I/O.  
6. **Make Argon2id parameters configurable** – future‑proof password hashing.  
7. **Add more unit tests for parsers** – especially for malformed input.  
8. **Profile and optimize hot paths** – reduce allocations in collectors.  

### **Long Term**
9. **Enhance sandboxing** – explore seccomp or container isolation.  
10. **Add support for TLS directly** – so users can enable HTTPS without a reverse proxy.  

---

## **Conclusion**  
Kula is a mature, well‑crafted monitoring tool. Its architecture is clean, performance considerations are evident, and security features (Landlock, Argon2id) show proactive thinking. The identified issues are minor and mostly relate to hardening and future‑proofing. With the recommended improvements, Kula can become an even more robust solution for production environments.  

The development team clearly follows good practices (linting, testing, benchmarks) – kudos for that. I look forward to seeing the project evolve.
