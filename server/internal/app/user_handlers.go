package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

func (s *Server) handleListUsers(c *gin.Context) {
	page, size := httpx.Paging(c)
	q := strings.TrimSpace(c.Query("q"))
	role := c.Query("role")
	status := c.Query("status")

	clause := "1=1"
	args := []any{}
	n := 0
	if q != "" {
		n++
		args = append(args, "%"+q+"%")
		clause += fmt.Sprintf(" AND (username ILIKE $%d OR display_name ILIKE $%d)", n, n)
	}
	if role != "" {
		n++
		args = append(args, role)
		clause += fmt.Sprintf(" AND role=$%d", n)
	}
	if status != "" {
		n++
		args = append(args, status)
		clause += fmt.Sprintf(" AND status=$%d", n)
	}

	var total int
	countSQL := "SELECT count(*) FROM users WHERE " + clause
	if err := s.pool().QueryRow(c.Request.Context(), countSQL, args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	args = append(args, size, (page-1)*size)
	sql := `SELECT id, username, display_name, role, status, must_change_pwd,
	               last_login_at, created_at
	          FROM users WHERE ` + clause + `
	         ORDER BY id DESC LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool().Query(c.Request.Context(), sql, args...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []UserDTO{}
	for rows.Next() {
		var u UserDTO
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &u.Status,
			&u.MustChange, &u.LastLoginAt, &u.CreatedAt); err != nil {
			httpx.Fail(c, err)
			return
		}
		items = append(items, u)
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) handleCreateUser(c *gin.Context) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	fields := map[string]string{}
	if len(in.Username) < 3 {
		fields["username"] = "账号至少 3 个字符"
	}
	if in.DisplayName == "" {
		fields["display_name"] = "请填写姓名"
	}
	if in.Role != "admin" && in.Role != "reviewer" && in.Role != "annotator" {
		fields["role"] = "角色不合法"
	}
	if len(in.Password) < 8 {
		fields["password"] = "密码至少 8 位"
	}
	if len(fields) > 0 {
		httpx.Fail(c, httpx.Validation(fields, "表单校验失败"))
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	me := auth.MustCurrent(c)
	var id int64
	err = s.pool().QueryRow(c.Request.Context(),
		`INSERT INTO users (username, password_hash, display_name, role, must_change_pwd, created_by)
		 VALUES ($1,$2,$3,$4,true,$5) RETURNING id`,
		in.Username, hash, in.DisplayName, in.Role, me.ID).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			httpx.Fail(c, httpx.Conflict("账号已存在"))
			return
		}
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "user.create", "user", id, map[string]any{"username": in.Username, "role": in.Role})
	httpx.OK(c, gin.H{"id": id})
}

func (s *Server) handleUpdateUser(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Status      *string `json:"status"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	me := auth.MustCurrent(c)
	if id == me.ID && in.Status != nil && *in.Status != "active" {
		httpx.Fail(c, httpx.Conflict("不能停用自己的账号"))
		return
	}
	ctx := c.Request.Context()
	tag, err := s.pool().Exec(ctx,
		`UPDATE users SET
		   display_name = COALESCE($2, display_name),
		   role = COALESCE($3, role),
		   status = COALESCE($4, status),
		   token_version = token_version + CASE
		     WHEN ($3 IS NOT NULL AND $3 <> role) OR ($4 IS NOT NULL AND $4 <> status) THEN 1 ELSE 0 END,
		   updated_at = now()
		 WHERE id=$1`, id, in.DisplayName, in.Role, in.Status)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(c, httpx.NotFound("用户不存在"))
		return
	}
	s.audit(c, "user.update", "user", id, map[string]any{"role": in.Role, "status": in.Status})
	httpx.OK(c, gin.H{"ok": true})
}

func (s *Server) handleResetPassword(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	buf := make([]byte, 9)
	_, _ = rand.Read(buf)
	newPassword := "Fl" + base64.RawURLEncoding.EncodeToString(buf)
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	tag, err := s.pool().Exec(c.Request.Context(),
		`UPDATE users SET password_hash=$2, must_change_pwd=true,
		        token_version=token_version+1, updated_at=now() WHERE id=$1`, id, hash)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(c, httpx.NotFound("用户不存在"))
		return
	}
	s.audit(c, "user.reset_password", "user", id, nil)
	httpx.OK(c, gin.H{"new_password": newPassword})
}

func (s *Server) handleUserStats(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	row := s.pool().QueryRow(c.Request.Context(), `
		SELECT
		  (SELECT count(*) FROM claims WHERE user_id=$1),
		  (SELECT COALESCE(sum(granted),0) FROM claims WHERE user_id=$1),
		  count(*) FILTER (WHERE status='submitted'),
		  count(*) FILTER (WHERE status='approved'),
		  count(*) FILTER (WHERE status='rejected'),
		  count(*) FILTER (WHERE status='assigned')
		  FROM images WHERE annotator_id=$1`, id)
	var claims, granted, submitted, approved, rejected, assigned int
	if err := row.Scan(&claims, &granted, &submitted, &approved, &rejected, &assigned); err != nil {
		httpx.Fail(c, err)
		return
	}
	var avg float64
	_ = s.pool().QueryRow(c.Request.Context(),
		`SELECT COALESCE(avg(total),0) FROM annotations WHERE annotator_id=$1 AND kind='score'`, id).Scan(&avg)
	httpx.OK(c, gin.H{
		"claims": claims, "granted": granted, "submitted": submitted,
		"approved": approved, "rejected": rejected, "assigned": assigned,
		"avg_total": avg,
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
