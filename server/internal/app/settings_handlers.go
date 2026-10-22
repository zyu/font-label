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
		`SELECT id, name, is_active, created_at, dims::text, grade_bands::text
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
			bands     string
		)
		if err := rows.Scan(&id, &name, &active, &createdAt, &dims, &bands); err != nil {
			httpx.Fail(c, err)
			return
		}
		var dv, bv any
		_ = json.Unmarshal([]byte(dims), &dv)
		_ = json.Unmarshal([]byte(bands), &bv)
		items = append(items, gin.H{
			"id": id, "name": name, "is_active": active, "created_at": createdAt,
			"dims": dv, "grade_bands": bv,
		})
	}
	httpx.OK(c, gin.H{"items": items})
}

func (s *Server) handleCreateRubric(c *gin.Context) {
	var in struct {
		Name          string       `json:"name"`
		Dims          []score.Dim  `json:"dims"`
		Bands         []score.Band `json:"grade_bands"`
		CommentMin    int          `json:"comment_min"`
		CommentMax    int          `json:"comment_max"`
		AllowOverride bool         `json:"allow_total_override"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	fields := map[string]string{}
	if strings.TrimSpace(in.Name) == "" {
		fields["name"] = "请填写方案名称"
	}
	if len(in.Dims) == 0 {
		fields["dims"] = "至少一个维度"
	}
	var weightSum float64
	for _, d := range in.Dims {
		if d.Key == "" || d.Label == "" {
			fields["dims"] = "维度缺少 key 或名称"
		}
		weightSum += d.Weight
	}
	if weightSum <= 0 {
		fields["dims"] = "权重之和必须大于 0"
	}
	if len(in.Bands) == 0 {
		fields["grade_bands"] = "至少一个等级区间"
	}
	if in.CommentMin <= 0 {
		in.CommentMin = 20
	}
	if in.CommentMax <= in.CommentMin {
		in.CommentMax = in.CommentMin + 30
	}
	if len(fields) > 0 {
		httpx.Fail(c, httpx.Validation(fields, "评分方案校验失败"))
		return
	}
	dimsJSON, _ := json.Marshal(in.Dims)
	bandsJSON, _ := json.Marshal(in.Bands)
	var id int64
	err := s.pool().QueryRow(c.Request.Context(),
		`INSERT INTO rubrics (name, dims, grade_bands, comment_min, comment_max, allow_total_override, is_active)
		 VALUES ($1,$2::jsonb,$3::jsonb,$4,$5,$6,false) RETURNING id`,
		in.Name, dimsJSON, bandsJSON, in.CommentMin, in.CommentMax, in.AllowOverride).Scan(&id)
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
			`UPDATE rubrics SET is_active=true WHERE id=$1`, id)
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

var writableSettings = map[string]bool{
	"lock_ttl_hours": true, "max_hold_per_user": true, "claim_default_count": true,
	"max_claim_count": true, "max_upload_files": true, "max_file_mb": true,
	"auto_next_after_submit": true, "exception_reasons": true,
	"reject_reason_tags": true, "comment_templates": true,
}

func (s *Server) handlePutSetting(c *gin.Context) {
	key := c.Param("key")
	if !writableSettings[key] {
		httpx.Fail(c, httpx.Validation(map[string]string{"key": "不可修改的配置项"}, "配置项不可写"))
		return
	}
	body, err := c.GetRawData()
	if err != nil || !json.Valid(body) {
		httpx.Fail(c, httpx.BadRequest("配置值必须是合法 JSON"))
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
	page, size := httpx.Paging(c)
	rows, err := s.pool().Query(c.Request.Context(),
		`SELECT a.id, COALESCE(u.display_name,'系统'), a.action, a.target_type,
		        COALESCE(a.target_id,0), a.detail::text, a.created_at
		   FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_id
		  ORDER BY a.id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id         int64
			who, act   string
			typ        string
			targetID   int64
			detail     string
			createdAt  any
		)
		if err := rows.Scan(&id, &who, &act, &typ, &targetID, &detail, &createdAt); err != nil {
			httpx.Fail(c, err)
			return
		}
		var dv any
		_ = json.Unmarshal([]byte(detail), &dv)
		items = append(items, gin.H{
			"id": id, "actor": who, "action": act, "target_type": typ,
			"target_id": targetID, "detail": dv, "created_at": createdAt,
		})
	}
	httpx.OK(c, gin.H{"items": items, "page": page, "page_size": size})
}
