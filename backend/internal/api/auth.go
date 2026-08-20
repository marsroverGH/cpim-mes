package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cpim-mes/backend/internal/service"
)

// ====================================================================
// JWT auth middleware + role-based authorization
// ====================================================================

type ctxKey string

const ctxKeyClaims ctxKey = "claims"

// authMiddleware — Authorization: Bearer <token> を検証して context に Claims を埋める
// (chi.Group 内でのみ有効化されるため、パブリックエンドポイントは別ルートに配置すること)
func authMiddleware(auth *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				writeError(w, http.StatusUnauthorized,
					strErr("missing or malformed Authorization header"))
				return
			}
			tok := strings.TrimPrefix(h, "Bearer ")
			claims, err := auth.VerifyCurrent(r.Context(), tok)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type strErr string

func (s strErr) Error() string { return string(s) }

// ====================================================================
// /api/auth/login
// ====================================================================

func (h *server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := h.s.Auth.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		if err == service.ErrInvalidCredentials {
			writeError(w, 401, err)
			return
		}
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *server) me(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Context().Value(ctxKeyClaims).(*service.Claims)
	if c == nil {
		writeError(w, 401, strErr("not authenticated"))
		return
	}
	writeJSON(w, 200, map[string]any{
		"username":    c.Username,
		"role":        c.Role,
		"userId":      c.UserID,
		"permissions": permissionsForRole(c.Role),
	})
}

// ====================================================================
// /api/abc-analysis
// ====================================================================

func (h *server) runABC(w http.ResponseWriter, r *http.Request) {
	var (
		rows any
		err  error
	)
	if raw := strings.TrimSpace(r.URL.Query().Get("asOf")); raw != "" {
		asOf, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, strErr("asOf must be YYYY-MM-DD"))
			return
		}
		rows, err = h.s.ABC.RunAsOf(r.Context(), asOf)
	} else {
		rows, err = h.s.ABC.Run(r.Context())
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

// ====================================================================
// /api/items/export.csv  &  /api/items/import (multipart)
// ====================================================================

func (h *server) exportItemsCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="items.csv"`)
	if err := h.s.CSV.ExportItems(r.Context(), w); err != nil {
		// header already sent — fallback log
		_ = err
	}
}

func (h *server) importItemsCSV(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, 400, err)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, err)
		return
	}
	defer file.Close()
	res, err := h.s.CSV.ImportItems(r.Context(), file)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}
