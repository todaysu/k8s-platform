package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

type userContextKey struct{}

// WithUser carries the authenticated user to handlers without exposing the
// session cookie or database lookup to every module.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey{}).(User)
	return user, ok
}

type attempt struct {
	Count int
	Until time.Time
}

// RouteRule declares the privilege a single route requires. Authorization is
// looked up by the route pattern Go's mux matched, so a route can never be
// granted or denied by accident of its URL spelling.
type RouteRule struct {
	// MinRole is the lowest role allowed to call the route.
	MinRole string
	// WebSocket marks routes upgraded to a bidirectional protocol. Those always
	// require the strict Origin check because the browser does not apply the
	// same-origin policy to a handshake.
	WebSocket bool
	// AdminOnlyModeQuery, when set with AdminOnlyModeValue, raises the
	// requirement to admin.
	AdminOnlyModeQuery string
	AdminOnlyModeValue string
}

// RouteAccess is the lookup table installed by the HTTP layer at startup.
type RouteAccess interface {
	// Lookup returns the rule for a mux pattern such as
	// "GET /api/v1/clusters/{id}/pods/{namespace}/{name}/exec".
	Lookup(pattern string) (RouteRule, bool)
}

// IsWebSocketUpgrade reports whether the request is a protocol upgrade.
func IsWebSocketUpgrade(r *http.Request) bool {
	return isWebSocketUpgrade(r)
}

func isWebSocketUpgrade(r *http.Request) bool {
	for _, value := range r.Header.Values("Connection") {
		if strings.EqualFold(strings.TrimSpace(value), "upgrade") {
			return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		}
	}
	return false
}

// RoleAllows reports whether a role meets the minimum required role.
func RoleAllows(userRole, required string) bool {
	if required == "" {
		return true
	}
	rank := map[string]int{"viewer": 1, "operator": 2, "admin": 3}
	return rank[userRole] >= rank[required]
}

// maxTrackedAttempts bounds the in-memory throttle table. When the bound is
// reached the oldest entries are evicted instead of rejecting every caller,
// otherwise one flood would permanently lock out all new clients.
const maxTrackedAttempts = 10000

// attemptWindow and maxAttemptsPerWindow apply per source IP.
const (
	attemptWindow      = 15 * time.Minute
	maxAttemptsPerIP   = 20
	maxAttemptsPerUser = 10
	// accountWindow is longer than attemptWindow: a distributed attacker must
	// not be able to walk the per-account budget by rotating source addresses.
	accountWindow = time.Hour
)

// clientKey identifies the caller. When the platform runs behind an ingress or
// load balancer, RemoteAddr is the proxy address and every user shares one
// bucket, so a trusted X-Forwarded-For is used when configured.
func (s *Service) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.trustProxyHeaders {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			if candidate := strings.TrimSpace(first); candidate != "" {
				// Only a syntactically valid IP is trusted, so a malformed
				// header cannot collapse every caller into one bucket.
				if net.ParseIP(candidate) != nil {
					host = candidate
				}
			}
		}
	}
	return host
}

// boundedPrune removes expired entries and, if still at capacity, evicts the
// entry closest to expiry so the table stays usable under sustained load.
func boundedPrune(now time.Time, table map[string]attempt, capacity int) {
	for k, v := range table {
		if now.After(v.Until) {
			delete(table, k)
		}
	}
	if len(table) < capacity {
		return
	}
	oldestKey, oldestAt := "", time.Time{}
	for k, v := range table {
		if oldestKey == "" || v.Until.Before(oldestAt) {
			oldestKey, oldestAt = k, v.Until
		}
	}
	delete(table, oldestKey)
}

func (s *Service) bump(table map[string]attempt, key string, window time.Duration, limit int, now time.Time) bool {
	v := table[key]
	if v.Count == 0 || now.After(v.Until) {
		v = attempt{Until: now.Add(window)}
	}
	v.Count++
	table[key] = v
	return v.Count > limit
}

// limited throttles a login attempt by source address and, when known, by
// account name. It reports true only when the request must be rejected; it
// always records the attempt so a flooding source cannot bypass accounting.
func (s *Service) limited(r *http.Request, username string) bool {
	now := time.Now()
	key := s.clientKey(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	boundedPrune(now, s.attempts, maxTrackedAttempts)
	blocked := s.bump(s.attempts, key, attemptWindow, maxAttemptsPerIP, now)
	if username != "" {
		boundedPrune(now, s.accountAttempts, maxTrackedAttempts)
		if s.bump(s.accountAttempts, strings.ToLower(username), accountWindow, maxAttemptsPerUser, now) {
			blocked = true
		}
	}
	return blocked
}

type Service struct {
	db                                 *sql.DB
	ownsDB                             bool
	mu                                 sync.Mutex
	attempts                           map[string]attempt
	accountAttempts                    map[string]attempt
	trustProxyHeaders                  bool
	access                             RouteAccess
	secure                             bool
	ttl                                time.Duration
	bootstrap, bootstrapPath, username string
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)

func New(path, username, hash string, secure bool, ttl time.Duration) (*Service, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS auth_users(id INTEGER PRIMARY KEY AUTOINCREMENT,username TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL,role TEXT NOT NULL,disabled INTEGER NOT NULL DEFAULT 0); CREATE TABLE IF NOT EXISTS auth_sessions(token_hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL,expires_at INTEGER NOT NULL); CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiry ON auth_sessions(expires_at)`); err != nil {
		db.Close()
		return nil, err
	}
	s, err := NewWithDB(db, filepath.Join(filepath.Dir(path), "bootstrap-token"), username, hash, secure, ttl)
	if err != nil {
		db.Close()
		return nil, err
	}
	s.ownsDB = true
	return s, nil
}

// NewWithDB uses platform-managed storage. The caller owns db and must have
// initialized auth_users and auth_sessions through the platform migrations.
func NewWithDB(db *sql.DB, bootstrapPath, username, hash string, secure bool, ttl time.Duration) (*Service, error) {
	s := &Service{db: db, attempts: map[string]attempt{}, accountAttempts: map[string]attempt{}, secure: secure, ttl: ttl, username: username, bootstrapPath: bootstrapPath}
	var count int
	var err error
	if err = db.QueryRow(`SELECT COUNT(*) FROM auth_users`).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		if _, err = bcrypt.Cost([]byte(hash)); err == nil {
			_, err = db.Exec(`INSERT INTO auth_users(username,password_hash,role) VALUES(?,?,'admin')`, username, hash)
		} else {
			s.bootstrap, err = randomToken()
			if err == nil {
				err = os.WriteFile(s.bootstrapPath, []byte(s.bootstrap+"\n"), 0600)
				if err == nil {
					err = os.Chmod(s.bootstrapPath, 0600)
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Service) Close() error {
	if s.ownsDB {
		return s.db.Close()
	}
	return nil
}

// TrustProxyHeaders enables X-Forwarded-For parsing for the login throttle.
// Leave it off unless a trusted reverse proxy or ingress sets that header:
// otherwise any client could forge the value and bypass per-source limiting.
func (s *Service) TrustProxyHeaders(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trustProxyHeaders = enabled
}

// SetRouteAccess installs the authorization table built by the HTTP layer.
func (s *Service) SetRouteAccess(access RouteAccess) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.access = access
}

// isTerminalRequest reports whether a GET request would open an interactive
// session. It is only consulted while no authorization table is installed, so
// that a missing table cannot hand a shell to a read-only role.
func isTerminalRequest(r *http.Request) bool {
	if isWebSocketUpgrade(r) {
		return true
	}
	return strings.HasSuffix(r.URL.Path, "/exec") || strings.HasSuffix(r.URL.Path, "/tty")
}

// routeAccessInstalled reports whether the HTTP layer supplied a table.
func (s *Service) routeAccessInstalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.access != nil
}

// lookupRoute resolves the authorization rule, tolerating an unset table.
func (s *Service) lookupRoute(pattern string) (RouteRule, bool) {
	s.mu.Lock()
	access := s.access
	s.mu.Unlock()
	if access == nil {
		return RouteRule{}, false
	}
	return access.Lookup(pattern)
}

// isDeclaredWebSocket reports whether the matched route is a declared terminal.
func (s *Service) isDeclaredWebSocket(r *http.Request) bool {
	s.mu.Lock()
	access := s.access
	s.mu.Unlock()
	if access == nil {
		return false
	}
	rule, ok := access.Lookup(r.Method + " " + r.Pattern)
	return ok && rule.WebSocket
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func reply(w http.ResponseWriter, status int, data any, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	var e any
	if code != "" {
		e = map[string]string{"code": code, "message": code}
	}
	json.NewEncoder(w).Encode(map[string]any{"data": data, "error": e})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		reply(w, 400, nil, "INVALID_REQUEST")
		return false
	}
	return true
}
func sameOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host
}
func (s *Service) current(r *http.Request) (User, bool) {
	c, err := r.Cookie("kubescope_session")
	if err != nil {
		return User{}, false
	}
	key := sha256.Sum256([]byte(c.Value))
	keyText := hex.EncodeToString(key[:])
	var userID, expiresAt int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT user_id,expires_at FROM auth_sessions WHERE token_hash=?`, keyText).Scan(&userID, &expiresAt); err != nil {
		return User{}, false
	}
	if time.Now().Unix() >= expiresAt {
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE token_hash=?`, keyText)
		return User{}, false
	}
	var u User
	err = s.db.QueryRowContext(r.Context(), `SELECT id,username,role,disabled FROM auth_users WHERE id=?`, userID).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled)
	return u, err == nil && !u.Disabled
}
func (s *Service) revoke(id int64) {
	_, _ = s.db.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, id)
}
func (s *Service) issue(w http.ResponseWriter, u User) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	now := time.Now()
	_, _ = s.db.Exec(`DELETE FROM auth_sessions WHERE expires_at<=?`, now.Unix())
	key := sha256.Sum256([]byte(token))
	if _, err := s.db.Exec(`INSERT INTO auth_sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, hex.EncodeToString(key[:]), u.ID, now.Add(s.ttl).Unix()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "kubescope_session", Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: int(s.ttl.Seconds())})
	return nil
}
func validPassword(p string) bool { return len(p) >= 12 && len(p) <= 72 }
func (s *Service) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		path := r.URL.Path
		if path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		// A WebSocket handshake is a GET request and is not protected by the
		// browser same-origin policy, so declared terminal routes always get the
		// strict Origin check regardless of method.
		if isWebSocketUpgrade(r) || s.isDeclaredWebSocket(r) || (r.Method != "GET" && r.Method != "HEAD") {
			if !sameOrigin(r) {
				reply(w, 403, nil, "ORIGIN_REJECTED")
				return
			}
		}
		if path == "/api/v1/auth/session" && r.Method == "GET" {
			u, ok := s.current(r)
			s.mu.Lock()
			setup := s.bootstrap != ""
			s.mu.Unlock()
			if !ok {
				reply(w, 401, map[string]any{"bootstrap_required": setup}, "UNAUTHENTICATED")
				return
			}
			reply(w, 200, u, "")
			return
		}
		if (path == "/api/v1/auth/login" || path == "/api/v1/auth/setup") && r.Method == "POST" {
			s.login(w, r, path)
			return
		}
		u, ok := s.current(r)
		if !ok {
			reply(w, 401, nil, "UNAUTHENTICATED")
			return
		}
		r = r.WithContext(WithUser(r.Context(), u))
		if path == "/api/v1/auth/logout" && r.Method == "POST" {
			if c, e := r.Cookie("kubescope_session"); e == nil {
				key := sha256.Sum256([]byte(c.Value))
				_, _ = s.db.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE token_hash=?`, hex.EncodeToString(key[:]))
			}
			http.SetCookie(w, &http.Cookie{Name: "kubescope_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
			reply(w, 200, true, "")
			return
		}
		if path == "/api/v1/auth/password" && r.Method == "PUT" {
			s.password(w, r, u)
			return
		}
		if strings.HasPrefix(path, "/api/v1/auth/users") {
			if u.Role != "admin" {
				reply(w, 403, nil, "FORBIDDEN")
				return
			}
			s.users(w, r, u)
			return
		}
		// Authorization is decided by the route's declared access requirement
		// rather than by matching the request path suffix. Suffix matching is a
		// second source of truth: it silently stops matching when a pattern gains
		// a sub-path, which would hand a privileged handler to a weaker role.
		//
		// When no table is installed the middleware refuses to reason about
		// individual routes: it cannot tell a terminal route from a read, so
		// only known-safe methods are allowed through. A missing table must
		// never widen access.
		if rule, ok := s.lookupRoute(r.Method + " " + r.Pattern); ok {
			if rule.AdminOnlyModeQuery != "" && r.URL.Query().Get(rule.AdminOnlyModeQuery) == rule.AdminOnlyModeValue && !RoleAllows(u.Role, "admin") {
				reply(w, 403, nil, "FORBIDDEN")
				return
			}
			if !RoleAllows(u.Role, rule.MinRole) {
				reply(w, 403, nil, "FORBIDDEN")
				return
			}
		} else if s.routeAccessInstalled() {
			// Declared read-only route: any authenticated role may read.
		} else if r.Method != "GET" && r.Method != "HEAD" && u.Role != "admin" {
			reply(w, 403, nil, "FORBIDDEN")
			return
		} else if r.Method == "GET" && isTerminalRequest(r) && !RoleAllows(u.Role, "operator") {
			// Without a table a GET route is assumed harmless, but a terminal
			// route hands over an interactive shell, so it stays restricted.
			reply(w, 403, nil, "FORBIDDEN")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) login(w http.ResponseWriter, r *http.Request, path string) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if !decode(w, r, &input) {
		return
	}
	// Throttle after decoding so the per-account budget can be enforced. The
	// body is capped by decode, and the request is still cheap to reject.
	if s.limited(r, input.Username) {
		reply(w, 429, nil, "LOGIN_RATE_LIMITED")
		return
	}
	if path == "/api/v1/auth/setup" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.bootstrap == "" || subtle.ConstantTimeCompare([]byte(input.Token), []byte(s.bootstrap)) != 1 {
			reply(w, 403, nil, "SETUP_REJECTED")
			return
		}
		if !validPassword(input.Password) || !usernamePattern.MatchString(input.Username) {
			reply(w, 400, nil, "INVALID_CREDENTIAL_FORMAT")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
		if err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		if _, err = s.db.ExecContext(r.Context(), `INSERT INTO auth_users(username,password_hash,role) VALUES(?,?,'admin')`, input.Username, string(hash)); err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		s.bootstrap = ""
		os.Remove(s.bootstrapPath)
		reply(w, 201, true, "")
		return
	}
	var u User
	var hash string
	err := s.db.QueryRowContext(r.Context(), `SELECT id,username,role,disabled,password_hash FROM auth_users WHERE username=?`, input.Username).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &hash)
	// Always perform a bcrypt comparison, including for unknown users.
	if err != nil {
		hash = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"
	}
	match := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) == nil
	if err != nil || !match || u.Disabled {
		reply(w, 401, nil, "INVALID_CREDENTIALS")
		return
	}
	if s.issue(w, u) != nil {
		reply(w, 503, nil, "AUTH_FAILED")
		return
	}
	reply(w, 200, u, "")
}
func (s *Service) password(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	var old string
	s.db.QueryRowContext(r.Context(), `SELECT password_hash FROM auth_users WHERE id=?`, u.ID).Scan(&old)
	if bcrypt.CompareHashAndPassword([]byte(old), []byte(in.Current)) != nil {
		reply(w, 403, nil, "INVALID_CREDENTIALS")
		return
	}
	if !validPassword(in.Password) {
		reply(w, 400, nil, "PASSWORD_LENGTH")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if _, err := s.db.ExecContext(r.Context(), `UPDATE auth_users SET password_hash=? WHERE id=?`, string(hash), u.ID); err != nil {
		reply(w, 500, nil, "AUTH_FAILED")
		return
	}
	s.revoke(u.ID)
	reply(w, 200, true, "")
}
func (s *Service) users(w http.ResponseWriter, r *http.Request, actor User) {
	if r.URL.Path != "/api/v1/auth/users" {
		reply(w, 404, nil, "NOT_FOUND")
		return
	}
	if r.Method == "GET" {
		rows, err := s.db.QueryContext(r.Context(), `SELECT id,username,role,disabled FROM auth_users ORDER BY id`)
		if err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		defer rows.Close()
		out := []User{}
		for rows.Next() {
			var u User
			if rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled) != nil {
				reply(w, 500, nil, "AUTH_FAILED")
				return
			}
			out = append(out, u)
		}
		if rows.Err() != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		reply(w, 200, out, "")
		return
	}
	var in struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != "admin" && in.Role != "operator" && in.Role != "viewer" {
		reply(w, 400, nil, "INVALID_ROLE")
		return
	}
	switch r.Method {
	case "POST":
		if !usernamePattern.MatchString(in.Username) || !validPassword(in.Password) {
			reply(w, 400, nil, "INVALID_CREDENTIAL_FORMAT")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
		if err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		var exists int
		if err = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM auth_users WHERE username=?`, in.Username).Scan(&exists); err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		if exists > 0 {
			reply(w, 409, nil, "USERNAME_EXISTS")
			return
		}
		if _, err = s.db.ExecContext(r.Context(), `INSERT INTO auth_users(username,password_hash,role,disabled) VALUES(?,?,?,?)`, in.Username, string(hash), in.Role, in.Disabled); err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		reply(w, 201, true, "")
	case "PATCH":
		if in.ID == actor.ID && (in.Disabled || in.Role != "admin") {
			reply(w, 400, nil, "CANNOT_DISABLE_SELF")
			return
		}
		if in.Password != "" && !validPassword(in.Password) {
			reply(w, 400, nil, "PASSWORD_LENGTH")
			return
		}
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		defer tx.Rollback()
		var res sql.Result
		if in.Password != "" {
			hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
			if e != nil {
				reply(w, 500, nil, "AUTH_FAILED")
				return
			}
			res, err = tx.ExecContext(r.Context(), `UPDATE auth_users SET role=?,disabled=?,password_hash=? WHERE id=?`, in.Role, in.Disabled, string(hash), in.ID)
		} else {
			res, err = tx.ExecContext(r.Context(), `UPDATE auth_users SET role=?,disabled=? WHERE id=?`, in.Role, in.Disabled, in.ID)
		}
		if err != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			reply(w, 404, nil, "NOT_FOUND")
			return
		}
		if tx.Commit() != nil {
			reply(w, 500, nil, "AUTH_FAILED")
			return
		}
		s.revoke(in.ID)
		reply(w, 200, true, "")
	default:
		reply(w, 405, nil, "METHOD_NOT_ALLOWED")
	}
}
