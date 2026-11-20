package app

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/httpx"
	"github.com/shangyue/font-label/server/internal/platform/score"
)

func (s *Server) handleActiveRubric(c *gin.Context) {
	r, err := s.activeRubric(c.Request.Context())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, r)
}

func (s *Server) handleListRubrics(c *gin.Context) {
	rows, err := s.pool().Query(c.Request.Context(),
		`SELECT id, name, is_active, created_at, dims::text
		   FROM rubrics ORDER BY id DESC`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id        int64
			name      string
			active    bool
			createdAt any
			dims      string
		)
		if err := rows.Scan(&id, &name, &active, &createdAt, &dims); err != nil {
			httpx.Fail(c, err)
			return
		}
		var dv any
		_ = json.Unmarshal([]byte(dims), &dv)
		items = append(items, gin.H{
			"id": id, "name": name, "is_active": active, "created_at": createdAt,
			"dims": dv, "levels": score.Levels,
		})
	}
	httpx.OK(c, gin.H{"items": items})
}

func (s *Server) handleCreateRubric(c *gin.Context) {
	var in struct {
		Name string `json:"name"`
		Dims []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"dims"`
		DimCommentMin int `json:"dim_comment_min"`
		DimCommentMax int `json:"dim_comment_max"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpx.Fail(c, httpx.Validation(map[string]string{"name": "请填写方案名称"}, "评级方案校验失败"))
		return
	}
	keys := map[string]bool{"white": true, "black": true, "variation": true, "structure": true, "control": true}
	for _, dim := range in.Dims {
		if !keys[dim.Key] || strings.TrimSpace(dim.Label) == "" {
			httpx.Fail(c, httpx.Validation(map[string]string{"dims": "五个固定维度必须各提供一次，并填写名称"}, "评级方案校验失败"))
			return
		}
		delete(keys, dim.Key)
	}
	if len(keys) != 0 {
		httpx.Fail(c, httpx.Validation(map[string]string{"dims": "请提供全部五个维度"}, "评级方案校验失败"))
		return
	}
	if in.DimCommentMin <= 0 {
		in.DimCommentMin = 10
	}
	if in.DimCommentMax <= 0 {
		in.DimCommentMax = 50
	}
	if in.DimCommentMax < in.DimCommentMin {
		httpx.Fail(c, httpx.Validation(map[string]string{"dim_comment_max": "每维评语上限不能小于下限"}, "评级方案校验失败"))
		return
	}
	dims, _ := json.Marshal(in.Dims)
	var id int64
	err := s.pool().QueryRow(c.Request.Context(),
		`INSERT INTO rubrics (name,dims,dim_comment_min,dim_comment_max,is_active)
		 VALUES ($1,$2::jsonb,$3,$4,false) RETURNING id`, in.Name, dims, in.DimCommentMin, in.DimCommentMax).Scan(&id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "rubric.create", "rubric", id, map[string]any{"name": in.Name})
	httpx.OK(c, gin.H{"id": id})
}

func (s *Server) handleActivateRubric(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	err = s.db.Tx(c.Request.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE rubrics SET is_active=false WHERE is_active=true`); err != nil {
			return err
		}
		tag, err := tx.Exec(c.Request.Context(),
			`UPDATE rubrics SET is_active=true WHERE id=$1 AND jsonb_array_length(dims)=5
			AND dims @> '[{"key":"white"},{"key":"black"},{"key":"variation"},{"key":"structure"},{"key":"control"}]' `, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("评分方案不存在")
		}
		return nil
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "rubric.activate", "rubric", id, nil)
	httpx.OK(c, gin.H{"ok": true})
}

func (s *Server) handleGetSettings(c *gin.Context) {
	rows, err := s.pool().Query(c.Request.Context(), `SELECT key, value::text FROM settings ORDER BY key`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	out := gin.H{}
	for rows.Next() {
		var k, raw string
		if err := rows.Scan(&k, &raw); err != nil {
			httpx.Fail(c, err)
			return
		}
		var v any
		_ = json.Unmarshal([]byte(raw), &v)
		out[k] = v
	}
	httpx.OK(c, gin.H{"settings": out})
}

func (s *Server) handlePutSetting(c *gin.Context) {
	key := c.Param("key")
	if _, ok := settingDefinitions[key]; !ok {
		httpx.Fail(c, httpx.Validation(map[string]string{"key": "不可修改的配置项"}, "配置项不可写"))
		return
	}
	body, err := c.GetRawData()
	if err != nil || !json.Valid(body) {
		httpx.Fail(c, httpx.BadRequest("配置值必须是合法 JSON"))
		return
	}
	if err := validateSetting(key, body); err != nil {
		httpx.Fail(c, err)
		return
	}
	_, err = s.pool().Exec(c.Request.Context(),
		`INSERT INTO settings (key, value, updated_at) VALUES ($1,$2::jsonb,now())
		 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, body)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	s.audit(c, "settings.update", "setting", 0, map[string]any{"key": key})
	httpx.OK(c, gin.H{"ok": true})
}

func (s *Server) handleAuditLogs(c *gin.Context) {
	ctx := c.Request.Context()
	page, size := listPaging(c)
	before := httpx.QueryInt64(c, "before")
	if before <= 0 {
		if err := s.pool().QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM audit_logs`).Scan(&before); err != nil {
			httpx.Fail(c, err)
			return
		}
	}
	from := ` FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_id
		WHERE a.id<=$1 AND ($2='' OR strpos(lower(a.action),lower($2))>0)
		  AND ($3='' OR strpos(lower(COALESCE(u.display_name,'')),lower($3))>0
		             OR strpos(lower(COALESCE(u.username,'')),lower($3))>0)`
	args := []any{before, c.Query("action"), c.Query("actor")}
	var total int
	if err := s.pool().QueryRow(ctx, "SELECT count(*)"+from, args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	rows, err := s.pool().Query(ctx,
		`SELECT a.id, COALESCE(u.display_name,'系统/匿名'), COALESCE(u.username,''), a.action, a.target_type,
		        COALESCE(a.target_id,0), a.detail, a.created_at, COALESCE(host(a.ip),'')`+from+
			` ORDER BY a.id DESC LIMIT $4 OFFSET $5`, append(args, size, (page-1)*size)...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, targetID int64
		var actor, username, action, targetType, ip string
		var detail, createdAt any
		if err := rows.Scan(&id, &actor, &username, &action, &targetType, &targetID, &detail, &createdAt, &ip); err != nil {
			httpx.Fail(c, err)
			return
		}
		items = append(items, gin.H{
			"id": id, "actor": actor, "username": username, "action": action, "target_type": targetType,
			"target_id": targetID, "detail": detail, "created_at": createdAt, "ip": ip,
		})
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "before": before, "page": page, "page_size": size})
}
