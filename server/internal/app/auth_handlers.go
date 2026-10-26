package app

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

// principalFrom is a thin wrapper so handlers keep a local type name.
func principalFrom(c *gin.Context) (principal, bool) {
	p, ok := auth.Current(c)
	if !ok {
		return principal{}, false
	}
	return principal{ID: p.ID, Username: p.Username, DisplayName: p.DisplayName, Role: p.Role}, true
}

type principal struct {
	ID          int64
	Username    string
	DisplayName string
	Role        string
}

func (p principal) isAdmin() bool { return p.Role == "admin" }

func (s *Server) handleLogin(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Username) == "" || in.Password == "" {
		httpx.Fail(c, httpx.Validation(map[string]string{"username": "请输入账号和密码"}, "账号或密码为空"))
		return
	}
	key := in.Username + "|" + c.ClientIP()
	if s.login.Blocked(key) {
		httpx.Fail(c, httpx.New(http.StatusTooManyRequests, "LOGIN_LOCKED", "登录失败次数过多，请 10 分钟后再试"))
		return
	}

	var (
		id                                        int64
		username, displayName, role, status, hash string
		tv                                        int
		mustChange                                bool
	)
	err := s.pool().QueryRow(c.Request.Context(),
		`SELECT id, username, display_name, role, status, password_hash, token_version, must_change_pwd
		   FROM users WHERE username=$1`, strings.TrimSpace(in.Username)).
		Scan(&id, &username, &displayName, &role, &status, &hash, &tv, &mustChange)
	if err != nil || !auth.VerifyPassword(hash, in.Password) {
		s.login.Fail(key)
		httpx.Fail(c, httpx.Unauthorized("账号或密码不正确"))
		return
	}
	if status != "active" {
		httpx.Fail(c, httpx.Forbidden("账号已被停用，请联系管理员"))
		return
	}
	s.login.Pass(key)

	if _, err := s.pool().Exec(c.Request.Context(),
		`UPDATE users SET last_login_at=now() WHERE id=$1`, id); err != nil {
		httpx.Fail(c, err)
		return
	}
	p := auth.Principal{ID: id, Username: username, DisplayName: displayName, Role: role}
	token, err := auth.Sign(s.cfg.JWTSecret, p, tv, s.cfg.AccessTTL)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "auth.login", "user", id, map[string]any{"username": username})

	httpx.OK(c, gin.H{
		"access_token": token,
		"expires_in":   int(s.cfg.AccessTTL.Seconds()),
		"user": gin.H{
			"id": id, "username": username, "display_name": displayName,
			"role": role, "must_change_pwd": mustChange,
		},
	})
}

func (s *Server) handleMe(c *gin.Context) {
	p := auth.MustCurrent(c)
	ctx := c.Request.Context()

	rubric, err := s.activeRubric(ctx)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var mustChange bool
	_ = s.pool().QueryRow(ctx, `SELECT must_change_pwd FROM users WHERE id=$1`, p.ID).Scan(&mustChange)

	httpx.OK(c, gin.H{
		"user": gin.H{
			"id": p.ID, "username": p.Username, "display_name": p.DisplayName,
			"role": p.Role, "must_change_pwd": mustChange,
		},
		"rubric":             rubric,
		"exception_reasons":  s.settingValue(ctx, "exception_reasons"),
		"reject_reason_tags": s.settingValue(ctx, "reject_reason_tags"),
		"comment_templates":  s.settingValue(ctx, "comment_templates"),
		"limits": gin.H{
			"max_hold_per_user":   s.settingInt(ctx, "max_hold_per_user", s.cfg.MaxHold),
			"lock_ttl_hours":      s.settingInt(ctx, "lock_ttl_hours", 24),
			"claim_default_count": s.settingInt(ctx, "claim_default_count", 10),
			"max_claim_count":     s.settingInt(ctx, "max_claim_count", 100),
			"max_upload_files":    s.settingInt(ctx, "max_upload_files", 200),
			"max_file_mb":         s.settingInt(ctx, "max_file_mb", int(s.cfg.MaxFileMB)),
			"auto_next":           s.settingInt(ctx, "auto_next_after_submit", 1) == 1,
		},
	})
}

func (s *Server) handleChangePassword(c *gin.Context) {
	p := auth.MustCurrent(c)
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	if len(in.NewPassword) < 8 {
		httpx.Fail(c, httpx.Validation(map[string]string{"new_password": "至少 8 位，含字母和数字"}, "新密码强度不足"))
		return
	}
	var hash string
	if err := s.pool().QueryRow(c.Request.Context(),
		`SELECT password_hash FROM users WHERE id=$1`, p.ID).Scan(&hash); err != nil {
		httpx.Fail(c, err)
		return
	}
	if !auth.VerifyPassword(hash, in.OldPassword) {
		httpx.Fail(c, httpx.Validation(map[string]string{"old_password": "原密码不正确"}, "原密码不正确"))
		return
	}
	newHash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var tv int
	err = s.pool().QueryRow(c.Request.Context(),
		`UPDATE users SET password_hash=$1, must_change_pwd=false, token_version=token_version+1, updated_at=now()
		   WHERE id=$2 RETURNING token_version`, newHash, p.ID).Scan(&tv)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	token, err := auth.Sign(s.cfg.JWTSecret, p, tv, s.cfg.AccessTTL)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "auth.password", "user", p.ID, nil)
	httpx.OK(c, gin.H{"access_token": token})
}

func (s *Server) handleLogout(c *gin.Context) {
	p := auth.MustCurrent(c)
	s.audit(c, "auth.logout", "user", p.ID, nil)
	httpx.OK(c, gin.H{"ok": true})
}
