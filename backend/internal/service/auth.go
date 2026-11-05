package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

const SessionTTL = 8 * time.Hour

const csrfTokenVersion = "v1"

type AuthService struct {
	repo   *repository.UserRepo
	secret []byte
}

func NewAuthService(repo *repository.UserRepo, secret string) *AuthService {
	return &AuthService{repo: repo, secret: []byte(secret)}
}

type Claims struct {
	UserID   string      `json:"uid"`
	Username string      `json:"usr"`
	Role     domain.Role `json:"role"`
	jwt.RegisteredClaims
}

type LoginResponse struct {
	Token    string      `json:"token"`
	Username string      `json:"username"`
	Role     domain.Role `json:"role"`
}

// Login authenticates username/password and issues a short-lived signed JWT.
// Browser handlers place this JWT only in an HttpOnly cookie; the token value is
// returned directly only by the explicit /api/auth/token endpoint for CLI/API use.
func (s *AuthService) Login(ctx context.Context, username, password string) (*LoginResponse, error) {
	u, err := s.repo.FindByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	now := time.Now()
	claims := Claims{
		UserID:   u.ID.String(),
		Username: u.Username,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(SessionTTL)),
			Issuer:    "cpim-mes",
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return nil, err
	}
	return &LoginResponse{Token: signed, Username: u.Username, Role: u.Role}, nil
}

// Verify validates a signed JWT and returns its claims.
func (s *AuthService) Verify(tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid || c.ExpiresAt == nil || c.Issuer != "cpim-mes" {
		return nil, errors.New("invalid token")
	}
	return c, nil
}

// VerifyCurrent verifies the JWT and then refreshes authorization attributes
// from the users table. This makes deactivation and role changes effective on
// the next request instead of leaving stale JWT privileges valid for 8 hours.
func (s *AuthService) VerifyCurrent(ctx context.Context, tokenStr string) (*Claims, error) {
	claims, err := s.Verify(tokenStr)
	if err != nil {
		return nil, err
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, errors.New("invalid token subject")
	}
	u, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.IsActive {
		return nil, errors.New("user is inactive or no longer exists")
	}
	claims.Username = u.Username
	claims.Role = u.Role
	return claims, nil
}

// NewCSRFToken creates a signed double-submit token bound to one exact session
// JWT. The random nonce is readable by browser JavaScript, but the session JWT
// remains HttpOnly. The signature prevents attacker-chosen cookie/header pairs.
func (s *AuthService) NewCSRFToken(sessionToken string) (string, error) {
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	sig := s.csrfMAC(sessionToken, nonce)
	return csrfTokenVersion + "." + nonce + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyCSRFToken validates a signed double-submit token against the exact JWT
// presented in the HttpOnly session cookie.
func (s *AuthService) VerifyCSRFToken(sessionToken, csrfToken string) bool {
	parts := strings.Split(csrfToken, ".")
	if len(parts) != 3 || parts[0] != csrfTokenVersion || parts[1] == "" || parts[2] == "" {
		return false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != parts[1] {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size || base64.RawURLEncoding.EncodeToString(provided) != parts[2] {
		return false
	}
	expected := s.csrfMAC(sessionToken, parts[1])
	return hmac.Equal(provided, expected)
}

func (s *AuthService) csrfMAC(sessionToken, nonce string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("cpim-mes-csrf:" + csrfTokenVersion + "\x00"))
	_, _ = mac.Write([]byte(sessionToken))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(nonce))
	return mac.Sum(nil)
}
