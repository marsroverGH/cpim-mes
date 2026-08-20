package service

import (
	"context"
	"errors"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/cpim-mes/backend/internal/repository"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

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

// Login — ユーザー名+パスワード検証→JWT発行
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
			ExpiresAt: jwt.NewNumericDate(now.Add(8 * time.Hour)),
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

// Verify — JWT を検証し Claims を返す
func (s *AuthService) Verify(tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
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
	// Always trust the current database values over potentially stale JWT
	// authorization claims.
	claims.Username = u.Username
	claims.Role = u.Role
	return claims, nil
}
