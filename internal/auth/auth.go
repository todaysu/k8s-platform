package auth

import (
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
type attempt struct {
	Count int
	Until time.Time
}
type Service struct {
	db                                 *sql.DB
	ownsDB                             bool
	mu                                 sync.Mutex
	attempts                           map[string]attempt
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
	s := &Service{db: db, attempts: map[string]attempt{}, secure: secure, ttl: ttl, username: username, bootstrapPath: bootstrapPath}
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
func (s *Service) limited(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.attempts {
		if now.After(v.Until) {
			delete(s.attempts, k)
		}
	}
	v := s.attempts[host]
	if v.Count == 0 {
		v.Until = now.Add(15 * time.Minute)
	}
	v.Count++
	if len(s.attempts) > 10000 {
		return true
	}
	s.attempts[host] = v
	return v.Count > 20
}
func (s *Service) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		path := r.URL.Path
		if path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		if (r.Method != "GET" && r.Method != "HEAD") || (strings.HasSuffix(path, "/exec") || strings.HasSuffix(path, "/kubectl/tty")) {
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
		if strings.HasSuffix(path, "/kubectl/tty") && u.Role != "admin" {
			reply(w, 403, nil, "FORBIDDEN")
			return
		}
		if strings.HasSuffix(path, "/exec") {
			if u.Role != "admin" && u.Role != "operator" {
				reply(w, 403, nil, "FORBIDDEN")
				return
			}
		} else if r.Method != "GET" && r.Method != "HEAD" && u.Role != "admin" {
			reply(w, 403, nil, "FORBIDDEN")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) login(w http.ResponseWriter, r *http.Request, path string) {
	if s.limited(r) {
		reply(w, 429, nil, "LOGIN_RATE_LIMITED")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if !decode(w, r, &input) {
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
