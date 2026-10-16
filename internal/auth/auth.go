package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"net/http"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie  = "flipbook_session"
	sessionTTL     = 7 * 24 * time.Hour // 1 week
	bcryptCost     = 12
	settingPwdHash = "admin_password_hash"
)

type Auth struct {
	db     *database.DB
	secret string
	tmpl   *template.Template
}

func New(db *database.DB, secret string, tmpl *template.Template) *Auth {
	return &Auth{db: db, secret: secret, tmpl: tmpl}
}

// SetPassword hashes and stores the admin password in MongoDB.
func (a *Auth) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	return a.db.SetSetting(settingPwdHash, string(hash))
}

// HasPassword returns true if an admin password has been set.
func (a *Auth) HasPassword() bool {
	_, err := a.db.GetSetting(settingPwdHash)
	return err == nil
}

// CheckPassword verifies a password against the stored hash.
func (a *Auth) CheckPassword(password string) bool {
	hash, err := a.db.GetSetting(settingPwdHash)
	if err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Login verifies credentials, creates a session, and sets the cookie.
func (a *Auth) Login(w http.ResponseWriter, password string) bool {
	if !a.CheckPassword(password) {
		return false
	}

	token := a.generateToken()
	if err := a.db.CreateSession(token, sessionTTL); err != nil {
		return false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false, // set true in production behind HTTPS
	})
	return true
}

// Logout deletes the session and clears the cookie.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		a.db.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

// IsAuthenticated checks if the current request has a valid session.
func (a *Auth) IsAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return a.db.ValidateSession(cookie.Value)
}

// RequireAuth is middleware that protects admin routes.
func (a *Auth) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If no password is set, admin is open (first-run experience)
		if !a.HasPassword() {
			next.ServeHTTP(w, r)
			return
		}

		if a.IsAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

// LoginPage renders the login form.
func (a *Auth) LoginPage(w http.ResponseWriter, r *http.Request) {
	if !a.HasPassword() {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	if a.IsAuthenticated(r) {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	a.tmpl.ExecuteTemplate(w, "login", map[string]interface{}{
		"Error": "",
	})
}

// LoginSubmit processes the login form.
func (a *Auth) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")

	if a.Login(w, password) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	// Rate-limit brute force with a small delay
	time.Sleep(500 * time.Millisecond)

	a.tmpl.ExecuteTemplate(w, "login", map[string]interface{}{
		"Error": "Invalid password",
	})
}

// LogoutHandler handles the logout action.
func (a *Auth) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	a.Logout(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *Auth) generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	mac := hmac.New(sha256.New, []byte(a.secret))
	mac.Write(b)
	sig := mac.Sum(nil)
	return hex.EncodeToString(b) + "." + hex.EncodeToString(sig)
}
