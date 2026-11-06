package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const recPassword = "Rec-Strong-Password-2026!"

func TestRecDBUserAdministrationLifecycle(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	ua := f.Svc.UserAdmin

	before, err := ua.List(f.Ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Validation errors.
	invalid := []CreateManagedUserInput{
		{Username: " ", Password: recPassword, Role: domain.RolePlanner},
		{Username: strings.Repeat("u", 129), Password: recPassword, Role: domain.RolePlanner},
		{Username: "bad\tname", Password: recPassword, Role: domain.RolePlanner},
		{Username: "short-pw", Password: "short", Role: domain.RolePlanner},
		{Username: "long-pw", Password: strings.Repeat("p", 73), Role: domain.RolePlanner},
		{Username: "nl-pw", Password: "Rec-Strong-Password\n2026", Role: domain.RolePlanner},
		{Username: "bad-role", Password: recPassword, Role: domain.Role("root")},
	}
	for i, in := range invalid {
		if _, err := ua.Create(f.Ctx, in); recCode(err) != domain.ErrCodeValidation {
			t.Fatalf("invalid input %d: %v", i, err)
		}
	}

	admin, err := ua.Create(f.Ctx, CreateManagedUserInput{Username: "  rec-root  ", Password: recPassword, Role: domain.RoleAdmin})
	if err != nil || admin.Username != "rec-root" || admin.Role != domain.RoleAdmin || !admin.IsActive {
		t.Fatalf("create admin: %v %+v", err, admin)
	}
	if admin.PasswordHash == recPassword {
		t.Fatal("password stored in plain text")
	}
	_, err = ua.Create(f.Ctx, CreateManagedUserInput{Username: "rec-root", Password: recPassword, Role: domain.RolePlanner})
	recWantCode(t, err, domain.ErrCodeConflict, "already exists")
	planner, err := ua.Create(f.Ctx, CreateManagedUserInput{Username: "rec-plan", Password: recPassword, Role: domain.RolePlanner})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := ua.List(f.Ctx)
	if len(after) != len(before)+2 {
		t.Fatalf("list: %d -> %d", len(before), len(after))
	}

	// Role / activation / not found.
	if u, err := ua.SetRole(f.Ctx, planner.ID, domain.RoleViewer); err != nil || u.Role != domain.RoleViewer {
		t.Fatalf("set role: %v", err)
	}
	_, err = ua.SetRole(f.Ctx, planner.ID, domain.Role("x"))
	recWantCode(t, err, domain.ErrCodeValidation, "")
	_, err = ua.SetRole(f.Ctx, uuid.New(), domain.RoleViewer)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	if u, err := ua.SetActive(f.Ctx, planner.ID, false); err != nil || u.IsActive {
		t.Fatalf("deactivate: %v", err)
	}
	_, err = ua.SetActive(f.Ctx, uuid.New(), false)
	recWantCode(t, err, domain.ErrCodeNotFound, "")

	// The last active administrator cannot be demoted or disabled.
	var activeAdmins int
	if err := f.DB.GetContext(f.Ctx, &activeAdmins, `SELECT count(*) FROM users WHERE role='admin' AND is_active`); err != nil {
		t.Fatal(err)
	}
	if activeAdmins == 1 {
		_, err = ua.SetRole(f.Ctx, admin.ID, domain.RolePlanner)
		recWantCode(t, err, domain.ErrCodeConflict, "last active administrator")
		_, err = ua.SetActive(f.Ctx, admin.ID, false)
		recWantCode(t, err, domain.ErrCodeConflict, "last active administrator")
	} else {
		t.Logf("seed contains %d active admins; last-admin guard exercised after demoting them", activeAdmins)
		if _, err := f.DB.ExecContext(f.Ctx, `UPDATE users SET is_active=false WHERE role='admin' AND id<>$1`, admin.ID); err != nil {
			t.Fatal(err)
		}
		_, err = ua.SetActive(f.Ctx, admin.ID, false)
		recWantCode(t, err, domain.ErrCodeConflict, "last active administrator")
	}

	// Password reset and self-service change.
	newPw := "Rec-Another-Password-2026"
	_, err = ua.ResetPassword(f.Ctx, planner.ID, "short")
	recWantCode(t, err, domain.ErrCodeValidation, "")
	_, err = ua.ResetPassword(f.Ctx, uuid.New(), newPw)
	recWantCode(t, err, domain.ErrCodeNotFound, "")
	if _, err := ua.ResetPassword(f.Ctx, admin.ID, newPw); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Svc.Auth.Login(f.Ctx, "rec-root", recPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password must stop working: %v", err)
	}
	if _, err := f.Svc.Auth.Login(f.Ctx, "rec-root", newPw); err != nil {
		t.Fatalf("reset password login: %v", err)
	}
	recWantCode(t, ua.ChangeOwnPassword(f.Ctx, admin.ID, "", recPassword), domain.ErrCodeValidation, "")
	recWantCode(t, ua.ChangeOwnPassword(f.Ctx, admin.ID, newPw, "short"), domain.ErrCodeValidation, "")
	recWantCode(t, ua.ChangeOwnPassword(f.Ctx, admin.ID, "wrong-current-password", recPassword), domain.ErrCodeValidation, "")
	if err := ua.ChangeOwnPassword(f.Ctx, admin.ID, newPw, recPassword); err != nil {
		t.Fatalf("change own password: %v", err)
	}
	if _, err := f.Svc.Auth.Login(f.Ctx, "rec-root", recPassword); err != nil {
		t.Fatalf("changed password login: %v", err)
	}
	if err := ua.ChangeOwnPassword(f.Ctx, uuid.New(), recPassword, newPw); err == nil {
		t.Fatal("unknown user must fail")
	}
}

func TestRecDBAuthTokens(t *testing.T) {
	t.Parallel()
	f := newDBFixture(t)
	auth := f.Svc.Auth
	u, err := f.Svc.UserAdmin.Create(f.Ctx, CreateManagedUserInput{Username: "rec-auth", Password: recPassword, Role: domain.RolePlanner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Login(f.Ctx, "nobody", recPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err := auth.Login(f.Ctx, "rec-auth", "Wrong-Password-123456"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	lr, err := auth.Login(f.Ctx, "rec-auth", recPassword)
	if err != nil || lr.Username != "rec-auth" || lr.Role != domain.RolePlanner || lr.Token == "" {
		t.Fatalf("login: %v %+v", err, lr)
	}
	c, err := auth.Verify(lr.Token)
	if err != nil || c.UserID != u.ID.String() || c.Subject != u.ID.String() || c.Issuer != "cpim-mes" {
		t.Fatalf("verify: %v %+v", err, c)
	}
	if ttl := c.ExpiresAt.Sub(c.IssuedAt.Time); ttl != SessionTTL {
		t.Fatalf("ttl %v", ttl)
	}
	cur, err := auth.VerifyCurrent(f.Ctx, lr.Token)
	if err != nil || cur.Role != domain.RolePlanner {
		t.Fatalf("verify current: %v", err)
	}

	// Role changes are reflected immediately from the DB, not the token.
	if _, err := f.Svc.UserAdmin.SetRole(f.Ctx, u.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if cur, err := auth.VerifyCurrent(f.Ctx, lr.Token); err != nil || cur.Role != domain.RoleViewer {
		t.Fatalf("role refresh: %v %+v", err, cur)
	}
	// Deactivation revokes existing tokens.
	if _, err := f.Svc.UserAdmin.SetActive(f.Ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.VerifyCurrent(f.Ctx, lr.Token); err == nil {
		t.Fatal("inactive user token must be rejected")
	}

	// Tampered / foreign / expired / wrong-alg / wrong-issuer tokens.
	if _, err := auth.Verify(lr.Token + "x"); err == nil {
		t.Fatal("tampered token accepted")
	}
	other := NewAuthService(f.Repos.Users, "another-secret-0123456789abcdef0123")
	if _, err := other.Verify(lr.Token); err == nil {
		t.Fatal("token signed with another secret accepted")
	}
	sign := func(method jwt.SigningMethod, key any, claims Claims) string {
		s, err := jwt.NewWithClaims(method, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	secret := []byte("test-only-jwt-secret-0123456789abcdef")
	now := time.Now()
	base := Claims{UserID: u.ID.String(), Username: u.Username, Role: u.Role, RegisteredClaims: jwt.RegisteredClaims{Issuer: "cpim-mes", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}
	expired := base
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Hour))
	noExp := base
	noExp.ExpiresAt = nil
	wrongIss := base
	wrongIss.Issuer = "evil"
	for name, tok := range map[string]string{
		"expired":   sign(jwt.SigningMethodHS256, secret, expired),
		"no-exp":    sign(jwt.SigningMethodHS256, secret, noExp),
		"issuer":    sign(jwt.SigningMethodHS256, secret, wrongIss),
		"alg-hs512": sign(jwt.SigningMethodHS512, secret, base),
	} {
		if _, err := auth.Verify(tok); err == nil {
			t.Fatalf("%s token accepted", name)
		}
	}
	if _, err := auth.Verify(sign(jwt.SigningMethodHS256, secret, base)); err != nil {
		t.Fatalf("well-formed token rejected: %v", err)
	}
	badSubject := base
	badSubject.UserID = "not-a-uuid"
	if _, err := auth.VerifyCurrent(f.Ctx, sign(jwt.SigningMethodHS256, secret, badSubject)); err == nil {
		t.Fatal("bad subject accepted")
	}
	ghost := base
	ghost.UserID = uuid.NewString()
	if _, err := auth.VerifyCurrent(f.Ctx, sign(jwt.SigningMethodHS256, secret, ghost)); err == nil {
		t.Fatal("deleted/unknown user accepted")
	}
	if _, err := auth.VerifyCurrent(f.Ctx, "garbage"); err == nil {
		t.Fatal("garbage accepted")
	}

	// CSRF tokens are bound to the session token.
	csrf, err := auth.NewCSRFToken(lr.Token)
	if err != nil || !auth.VerifyCSRFToken(lr.Token, csrf) {
		t.Fatalf("csrf: %v", err)
	}
	csrf2, _ := auth.NewCSRFToken(lr.Token)
	if csrf2 == csrf {
		t.Fatal("csrf tokens must be nonce-randomized")
	}
	parts := strings.Split(csrf, ".")
	for name, tok := range map[string]string{
		"other session": csrf,
		"version":       "v2." + parts[1] + "." + parts[2],
		"parts":         parts[0] + "." + parts[1],
		"empty nonce":   parts[0] + ".." + parts[2],
		"bad nonce b64": parts[0] + ".!!!." + parts[2],
		"short nonce":   parts[0] + ".AAAA." + parts[2],
		"bad sig b64":   parts[0] + "." + parts[1] + ".!!!",
		"short sig":     parts[0] + "." + parts[1] + ".AAAA",
		"swapped nonce": parts[0] + "." + strings.Split(csrf2, ".")[1] + "." + parts[2],
	} {
		session := lr.Token
		if name == "other session" {
			session = lr.Token + "-other"
		}
		if auth.VerifyCSRFToken(session, tok) {
			t.Fatalf("csrf %s accepted", name)
		}
	}
}

func TestRecDBAgentAnswersFromLiveData(t *testing.T) {
	t.Parallel()
	e := recNewEnv(t)
	ag := e.Svc.Agent
	// One overdue PO, one released WO due soon, one far-away released WO.
	recPO(t, e.dbFixture, "PO-REC-LATE", "CHAIN-1", 5, recDay(-20), recDay(-3))
	recPO(t, e.dbFixture, "PO-REC-ONTIME", "CHAIN-1", 5, recDay(-1), recDay(30))
	soon := recWO(t, e.dbFixture, "WO-REC-SOON", "WHEEL-1", 1, recDay(1), recDay(3))
	far := recWO(t, e.dbFixture, "WO-REC-FAR", "WHEEL-1", 1, recDay(20), recDay(25))
	for _, w := range []*domain.WorkOrder{soon, far} {
		if err := e.Repos.WorkOrders.UpdateStatus(e.Ctx, w.ID, "RELEASED"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := ag.Ask(e.Ctx, AgentRequest{Query: "   "}); err == nil {
		t.Fatal("empty query must fail")
	}
	ask := func(q, intent string) *AgentResponse {
		t.Helper()
		r, err := ag.Ask(e.Ctx, AgentRequest{Query: q})
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if r.Intent != intent || r.Summary == "" {
			t.Fatalf("%s: intent %s (want %s) summary %q", q, r.Intent, intent, r.Summary)
		}
		return r
	}

	r := ask("遅延中のPO", "PO_OVERDUE")
	if pos, ok := r.Data.([]domain.PurchaseOrder); !ok || len(pos) != 1 || pos[0].PONo != "PO-REC-LATE" {
		t.Fatalf("overdue POs: %#v", r.Data)
	}
	r = ask("今週完成するWO", "WO_DUE_SOON")
	if wos, ok := r.Data.([]domain.WorkOrder); !ok || len(wos) != 1 || wos[0].OrderNo != "WO-REC-SOON" {
		t.Fatalf("due-soon WOs: %#v", r.Data)
	}
	r = ask("30日以内に完成するWO", "WO_DUE_SOON")
	if wos, ok := r.Data.([]domain.WorkOrder); !ok || len(wos) != 2 {
		t.Fatalf("30-day WOs: %#v", r.Data)
	}
	r = ask("WHEEL-1 の在庫", "ITEM_STOCK")
	if !strings.Contains(r.Summary, "WHEEL-1") || !strings.Contains(r.Summary, "80") {
		t.Fatalf("stock summary: %s", r.Summary)
	}
	if _, err := ag.Ask(e.Ctx, AgentRequest{Query: "NOPE-999 の在庫"}); err == nil {
		t.Fatal("unknown item must fail")
	}
	ask("在庫", "ITEM_STOCK")
	ask("BIKE-100 の計画オーダ", "ITEM_PLANNED_ORDERS")
	ask("ABC分析を実行", "ABC")
	ask("KPIを表示", "KPI")
	ask("/help", "HELP")
	ask("天気を教えて", "UNKNOWN")
}
