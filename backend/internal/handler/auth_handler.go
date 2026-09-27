package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/identity"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type AuthHandlerOptions struct {
	DevMode      bool
	SecureCookie bool
	TrustProxy   bool
}
type AuthHandler struct {
	service  *identity.Service
	opts     AuthHandlerOptions
	attempts *identity.AttemptLimiter
}

func NewAuthHandler(service *identity.Service, opts AuthHandlerOptions) *AuthHandler {
	return &AuthHandler{service: service, opts: opts, attempts: identity.NewAttemptLimiter(4096)}
}
func (h *AuthHandler) Register(g *echo.Group) {
	g.GET("/auth/session", h.HandleSession)
	g.POST("/auth/login", h.HandleLogin)
	g.POST("/auth/logout", h.HandleLogout)
	g.POST("/auth/password", h.HandlePassword)
	g.POST("/auth/register", h.HandleRegister)
	g.POST("/auth/reset-password", h.HandleResetPassword)
	g.GET("/auth/invitation", h.HandleInvitation)
	g.POST("/auth/bootstrap", h.HandleBootstrap)
	g.GET("/admin/users", h.HandleUsers)
	g.PATCH("/admin/users/:id", h.HandleUpdateUser)
	g.GET("/admin/invitations", h.HandleInvitations)
	g.POST("/admin/invitations", h.HandleIssueInvitation)
	g.DELETE("/admin/invitations/:id", h.HandleRevokeInvitation)
}

type authSessionView struct {
	Mode          string         "json:\"mode\""
	Authenticated bool           "json:\"authenticated\""
	User          *identity.User "json:\"user,omitempty\""
	CSRFToken     string         "json:\"csrf_token,omitempty\""
	SetupRequired bool           "json:\"setup_required,omitempty\""
}

func accountBody(c echo.Context, target any) error {
	reader := http.MaxBytesReader(c.Response(), c.Request().Body, 16<<10)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return identity.ErrInvalid
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return identity.ErrInvalid
	}
	return nil
}
func accountHeaders(c echo.Context) {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
}
func accountError(c echo.Context, err error) error {
	accountHeaders(c)
	switch {
	case errors.Is(err, identity.ErrUnauthorized):
		return unauthorized(c, "邮箱、密码或邀请无效，请检查后重试")
	case errors.Is(err, identity.ErrForbidden):
		return forbidden(c, "需要管理员权限")
	case errors.Is(err, identity.ErrInvalid):
		return badRequest(c, "INVALID_ACCOUNT_INPUT", "输入无效；密码须至少 12 个字符、最多 72 个 UTF-8 字节，邮箱须为完整邮箱地址")
	case errors.Is(err, identity.ErrConflict):
		return conflict(c, "账号或邀请状态已变化，或操作将导致没有可用管理员，请刷新后重试")
	case errors.Is(err, identity.ErrRateLimited):
		c.Response().Header().Set("Retry-After", "60")
		return c.JSON(http.StatusTooManyRequests, APIResponse{Success: false, Error: &APIError{Code: "TOO_MANY_REQUESTS", Message: "请求过于频繁，请稍后重试"}})
	default:
		return serviceUnavailable(c, "账号服务暂时不可用")
	}
}
func (h *AuthHandler) publicAttempt(c echo.Context) error {
	accountHeaders(c)
	if h.opts.DevMode {
		return identity.ErrForbidden
	}
	if !h.attempts.Allow(authAttemptAddress(c.Request(), h.opts.TrustProxy), 100, 5*time.Minute) {
		return identity.ErrRateLimited
	}
	return nil
}

// authAttemptAddress trusts only a single IP supplied by an explicitly trusted,
// private/loopback reverse proxy. That proxy must overwrite X-Real-IP and be the
// only route to the API. X-Forwarded-For never controls authentication limits.
func authAttemptAddress(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if trustProxy && peer.Zone() == "" && (peer.IsPrivate() || peer.IsLoopback()) {
		values := r.Header.Values("X-Real-IP")
		if len(values) == 1 {
			client, err := netip.ParseAddr(strings.TrimSpace(values[0]))
			client = client.Unmap()
			if err == nil && client.Zone() == "" && !client.IsUnspecified() && !client.IsMulticast() {
				return client.String()
			}
		}
	}
	return peer.String()
}

func (h *AuthHandler) setSession(c echo.Context, issue identity.SessionIssue) error {
	accountHeaders(c)
	c.SetCookie(&http.Cookie{Name: identity.SessionCookieName, Value: issue.Token, Path: "/api/v1", HttpOnly: true, Secure: h.opts.SecureCookie, SameSite: http.SameSiteStrictMode, Expires: issue.Session.ExpiresAt, MaxAge: int(identity.SessionLifetime.Seconds())})
	return ok(c, authSessionView{Mode: "shared", Authenticated: true, User: &issue.Session.User, CSRFToken: issue.Session.CSRFToken})
}
func (h *AuthHandler) clearSession(c echo.Context) {
	c.SetCookie(&http.Cookie{Name: identity.SessionCookieName, Value: "", Path: "/api/v1", HttpOnly: true, Secure: h.opts.SecureCookie, SameSite: http.SameSiteStrictMode, Expires: time.Unix(1, 0), MaxAge: -1})
}
func (h *AuthHandler) HandleSession(c echo.Context) error {
	accountHeaders(c)
	if h.opts.DevMode {
		return ok(c, authSessionView{Mode: "local", Authenticated: true, User: &identity.User{Email: "", DisplayName: "本机管理员", Role: "admin"}})
	}
	if session := authmw.GetSession(c); session != nil {
		return ok(c, authSessionView{Mode: "shared", Authenticated: true, User: &session.User, CSRFToken: session.CSRFToken})
	}
	required, err := h.service.SetupRequired(c.Request().Context())
	if err != nil {
		return accountError(c, err)
	}
	return ok(c, authSessionView{Mode: "shared", Authenticated: false, SetupRequired: required})
}
func (h *AuthHandler) HandleLogin(c echo.Context) error {
	if err := h.publicAttempt(c); err != nil {
		return accountError(c, err)
	}
	var body struct {
		Email    string "json:\"email\""
		Password string "json:\"password\""
	}
	if err := accountBody(c, &body); err != nil {
		return accountError(c, err)
	}
	issue, err := h.service.Login(c.Request().Context(), body.Email, body.Password)
	if err != nil {
		return accountError(c, err)
	}
	return h.setSession(c, issue)
}
func (h *AuthHandler) HandleLogout(c echo.Context) error {
	accountHeaders(c)
	if !h.opts.DevMode {
		cookie, err := c.Cookie(identity.SessionCookieName)
		if err == nil {
			if err = h.service.Logout(c.Request().Context(), cookie.Value); err != nil {
				return accountError(c, err)
			}
		}
	}
	h.clearSession(c)
	return ok(c, authSessionView{Mode: "shared", Authenticated: false})
}
func (h *AuthHandler) HandlePassword(c echo.Context) error {
	accountHeaders(c)
	session := authmw.GetSession(c)
	if session == nil {
		return accountError(c, identity.ErrUnauthorized)
	}
	var body struct {
		Current string "json:\"current_password\""
		Next    string "json:\"new_password\""
	}
	if err := accountBody(c, &body); err != nil {
		return accountError(c, err)
	}
	if err := h.service.ChangePassword(c.Request().Context(), session.User, body.Current, body.Next); err != nil {
		return accountError(c, err)
	}
	h.clearSession(c)
	return ok(c, authSessionView{Mode: "shared", Authenticated: false})
}
func (h *AuthHandler) HandleRegister(c echo.Context) error {
	if err := h.publicAttempt(c); err != nil {
		return accountError(c, err)
	}
	var body struct {
		Token    string "json:\"token\""
		Password string "json:\"password\""
		Name     string "json:\"display_name\""
	}
	if err := accountBody(c, &body); err != nil {
		return accountError(c, err)
	}
	issue, err := h.service.Register(c.Request().Context(), body.Token, body.Password, body.Name)
	if err != nil {
		return accountError(c, err)
	}
	return h.setSession(c, issue)
}
func (h *AuthHandler) HandleResetPassword(c echo.Context) error {
	if err := h.publicAttempt(c); err != nil {
		return accountError(c, err)
	}
	var body struct {
		Token    string "json:\"token\""
		Password string "json:\"password\""
	}
	if err := accountBody(c, &body); err != nil {
		return accountError(c, err)
	}
	if err := h.service.ResetPassword(c.Request().Context(), body.Token, body.Password); err != nil {
		return accountError(c, err)
	}
	h.clearSession(c)
	return ok(c, authSessionView{Mode: "shared", Authenticated: false})
}
func (h *AuthHandler) HandleBootstrap(c echo.Context) error {
	if err := h.publicAttempt(c); err != nil {
		return accountError(c, err)
	}
	var body struct {
		Token    string "json:\"token\""
		Email    string "json:\"email\""
		Password string "json:\"password\""
		Name     string "json:\"display_name\""
	}
	if err := accountBody(c, &body); err != nil {
		return accountError(c, err)
	}
	issue, err := h.service.Bootstrap(c.Request().Context(), body.Token, body.Email, body.Password, body.Name)
	if err != nil {
		return accountError(c, err)
	}
	return h.setSession(c, issue)
}
func (h *AuthHandler) HandleInvitation(c echo.Context) error {
	if err := h.publicAttempt(c); err != nil {
		return accountError(c, err)
	}
	invitation, err := h.service.Invitation(c.Request().Context(), c.Request().Header.Get("X-Qraft-Invitation-Token"))
	if err != nil {
		return accountError(c, err)
	}
	return ok(c, map[string]any{"email": invitation.Email, "kind": invitation.Kind, "expires_at": invitation.ExpiresAt})
}
func accountAdmin(c echo.Context) (uuid.UUID, error) {
	accountHeaders(c)
	session := authmw.GetSession(c)
	if session == nil {
		return uuid.Nil, identity.ErrUnauthorized
	}
	if session.User.Role != "admin" || session.User.Disabled || session.User.ID == uuid.Nil {
		return uuid.Nil, identity.ErrForbidden
	}
	return session.User.ID, nil
}
func (h *AuthHandler) HandleUsers(c echo.Context) error {
	if _, err := accountAdmin(c); err != nil {
		return accountError(c, err)
	}
	users, err := h.service.ListUsers(c.Request().Context())
	if err != nil {
		return accountError(c, err)
	}
	return ok(c, map[string]any{"items": users})
}
func (h *AuthHandler) HandleUpdateUser(c echo.Context) error {
	actor, err := accountAdmin(c)
	if err != nil {
		return accountError(c, err)
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return accountError(c, identity.ErrInvalid)
	}
	var patch identity.UserPatch
	if err = accountBody(c, &patch); err != nil {
		return accountError(c, err)
	}
	user, err := h.service.UpdateUser(c.Request().Context(), actor, id, patch)
	if err != nil {
		return accountError(c, err)
	}
	return ok(c, user)
}
func (h *AuthHandler) HandleInvitations(c echo.Context) error {
	if _, err := accountAdmin(c); err != nil {
		return accountError(c, err)
	}
	invitations, err := h.service.ListInvitations(c.Request().Context())
	if err != nil {
		return accountError(c, err)
	}
	return ok(c, map[string]any{"items": invitations})
}
func (h *AuthHandler) HandleIssueInvitation(c echo.Context) error {
	actor, err := accountAdmin(c)
	if err != nil {
		return accountError(c, err)
	}
	var body struct {
		Email string "json:\"email\""
		Kind  string "json:\"kind\""
	}
	if err = accountBody(c, &body); err != nil {
		return accountError(c, err)
	}
	result, err := h.service.IssueInvitation(c.Request().Context(), actor, strings.TrimSpace(body.Email), body.Kind)
	if err != nil {
		return accountError(c, err)
	}
	return created(c, result)
}
func (h *AuthHandler) HandleRevokeInvitation(c echo.Context) error {
	actor, err := accountAdmin(c)
	if err != nil {
		return accountError(c, err)
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return accountError(c, identity.ErrInvalid)
	}
	if err = h.service.RevokeInvitation(c.Request().Context(), actor, id); err != nil {
		return accountError(c, err)
	}
	return noContent(c)
}
