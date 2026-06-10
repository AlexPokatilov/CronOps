// Package auth implements UI-user authentication for cronops-server.
// Users live in a Kubernetes Secret (bcrypt hashes), sessions are short-lived
// JWTs in an httpOnly cookie — no database involved.
package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// CookieName carries the session JWT.
	CookieName = "cronops_token"
	// TokenTTL is the session lifetime.
	TokenTTL = 12 * time.Hour
)

// Service validates credentials and issues/verifies session tokens.
type Service struct {
	client      client.Client
	namespace   string
	usersSecret string
	jwtSecret   string
	jwtKey      []byte // dev fallback / cached key
	devUsers    map[string]string
	secureCooky bool
	dummyHash   []byte
}

// Config for the auth service.
type Config struct {
	Client      client.Client
	Namespace   string // namespace holding the secrets, e.g. "cronops"
	UsersSecret string // secret with username -> bcrypt hash, default "cronops-users"
	JWTSecret   string // secret with key "key", default "cronops-jwt-key"
	// DevUsers maps username -> plaintext password; only for --dev mode.
	DevUsers     map[string]string
	SecureCookie bool
}

func New(cfg Config) (*Service, error) {
	s := &Service{
		client:      cfg.Client,
		namespace:   cfg.Namespace,
		usersSecret: cfg.UsersSecret,
		jwtSecret:   cfg.JWTSecret,
		devUsers:    cfg.DevUsers,
		secureCooky: cfg.SecureCookie,
	}
	if s.usersSecret == "" {
		s.usersSecret = "cronops-users"
	}
	if s.jwtSecret == "" {
		s.jwtSecret = "cronops-jwt-key"
	}
	if len(cfg.DevUsers) > 0 {
		// Dev mode: random per-process signing key, sessions die with the process.
		s.jwtKey = make([]byte, 32)
		if _, err := rand.Read(s.jwtKey); err != nil {
			return nil, err
		}
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("cronops-dummy"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	s.dummyHash = dummy
	return s, nil
}

// Authenticate checks username/password against dev users or the users Secret.
func (s *Service) Authenticate(ctx context.Context, username, password string) error {
	if len(s.devUsers) > 0 {
		if pw, ok := s.devUsers[username]; ok && pw == password {
			return nil
		}
		return fmt.Errorf("invalid credentials")
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: s.namespace, Name: s.usersSecret}
	if err := s.client.Get(ctx, key, &secret); err != nil {
		return fmt.Errorf("reading users secret: %w", err)
	}
	hash, ok := secret.Data[username]
	if !ok {
		// Burn comparable time to avoid trivially revealing valid usernames.
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

func (s *Service) signingKey(ctx context.Context) ([]byte, error) {
	if len(s.jwtKey) > 0 {
		return s.jwtKey, nil
	}
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: s.namespace, Name: s.jwtSecret}
	if err := s.client.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("reading jwt secret: %w", err)
	}
	k, ok := secret.Data["key"]
	if !ok || len(k) == 0 {
		return nil, fmt.Errorf("jwt secret %s has no %q entry", key, "key")
	}
	s.jwtKey = k
	return k, nil
}

// IssueCookie creates a session JWT for username, wrapped in an httpOnly cookie.
func (s *Service) IssueCookie(ctx context.Context, username string) (*http.Cookie, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   username,
		Issuer:    "cronops",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
	})
	signed, err := token.SignedString(key)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     CookieName,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCooky,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(TokenTTL.Seconds()),
	}, nil
}

// ClearCookie returns an expired cookie for logout.
func (s *Service) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCooky,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// Verify parses the session JWT and returns the username.
func (s *Service) Verify(ctx context.Context, tokenString string) (string, error) {
	key, err := s.signingKey(ctx)
	if err != nil {
		return "", err
	}
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}
	claims := token.Claims.(*jwt.RegisteredClaims)
	return claims.Subject, nil
}
