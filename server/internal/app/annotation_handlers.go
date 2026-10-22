package app

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

type submitInput struct {
	ClientSubmitID   string         `json:"client_submit_id"`
	Kind             string         `json:"kind"`
	Scores           map[string]int `json:"scores"`
	Comment          string         `json:"comment"`
	ExceptionReasons []string       `json:"exception_reasons"`
	ExceptionNote    string         `json:"exception_note"`
	DurationMS       int            `json:"duration_ms"`
	TotalOverride    *float64       `json:"total_override"`
}

func (s *Server) handleSaveDraft(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	var row struct {
		AnnotatorID int64
		Status      string
	}
	if err := s.pool().QueryRow(ctx,
		`SELECT COALESCE(annotator_id,0), status FROM images WHERE id=$1`, id).
		Scan(&row.AnnotatorID, &row.Status); err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if row.AnnotatorID != me.ID {
		httpx.Fail(c, httpx.Forbidden("这不是你的任务"))
		return
	}
	body, err := c.GetRawData()
	if err != nil || !json.Valid(body) {
		httpx.Fail(c, httpx.BadRequest("草稿内容不是合法 JSON"))
		return
	}
	_, err = s.pool().Exec(ctx,
		`INSERT INTO drafts (user_id, image_id, payload, updated_at) VALUES ($1,$2,$3::jsonb,now())
		 ON CONFLICT (user_id, image_id) DO UPDATE SET payload=EXCLUDED.payload, updated_at=now()`,
		me.ID, id, body)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"saved_at": "now"})
}

func (s *Server) handleGetDraft(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	me := auth.MustCurrent(c)
	var raw string
	err = s.pool().QueryRow(c.Request.Context(),
		`SELECT payload::text FROM drafts WHERE user_id=$1 AND image_id=$2`, me.ID, id).Scan(&raw)
	if err != nil {
		httpx.OK(c, gin.H{"draft": nil})
		return
	}
	var v any
	_ = json.Unmarshal([]byte(raw), &v)
	httpx.OK(c, gin.H{"draft": v})
}

func (s *Server) handleSubmit(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var in submitInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()

	rubric, err := s.activeRubric(ctx)
	if err != nil {
		httpx.Fail(c, err)
		return
	}

	// idempotency: same client_submit_id returns the previous result
	if in.ClientSubmitID != "" {
		var imgID int64
		var ver int
		var total float64
		err := s.pool().QueryRow(ctx,
			`SELECT image_id, version, COALESCE(total,0) FROM annotations WHERE client_submit_id=$1`,
			in.ClientSubmitID).Scan(&imgID, &ver, &total)
		if err == nil {
			if imgID != id {
				httpx.Fail(c, httpx.Conflict("提交标识已被其他任务使用"))
				return
			}
			httpx.OK(c, gin.H{"duplicate": true, "version": ver, "total": total})
			return
		}
	}

	var (
		status      string
		annotatorID int64
	)
	if err := s.pool().QueryRow(ctx,
		`SELECT status, COALESCE(annotator_id,0) FROM images WHERE id=$1`, id).
		Scan(&status, &annotatorID); err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if annotatorID != me.ID {
		httpx.Fail(c, httpx.Forbidden("这不是你的任务"))
		return
	}
	if status != "assigned" && status != "rejected" {
		httpx.Fail(c, httpx.Conflict("该任务当前状态不能提交（可能已被回收）"))
		return
	}

	kind := in.Kind
	if kind != "score" && kind != "exception" {
		httpx.Fail(c, httpx.Validation(map[string]string{"kind": "score 或 exception"}, "标注类型不合法"))
		return
	}

	var (
		total      float64
		grade      string
		comment    = strings.TrimSpace(in.Comment)
		reasons    = in.ExceptionReasons
		scoresJSON []byte
	)
	if reasons == nil {
		reasons = []string{}
	}
	if kind == "score" {
		if err := rubric.ValidateScores(in.Scores); err != nil {
			httpx.Fail(c, httpx.Validation(map[string]string{"scores": err.Error()}, err.Error()))
			return
		}
		n := utf8.RuneCountInString(comment)
		if n < rubric.CommentMin || n > rubric.CommentMax {
			httpx.Fail(c, httpx.Validation(map[string]string{"comment": "点评需 " + itoa(rubric.CommentMin) + "~" + itoa(rubric.CommentMax) + " 字"},
				"点评字数不符合要求（当前 "+itoa(n)+" 字）"))
			return
		}
		total, grade = rubric.Total(in.Scores)
		if in.TotalOverride != nil {
			if !rubric.AllowTotalOver {
				httpx.Fail(c, httpx.Validation(map[string]string{"total_override": "当前方案不允许手填总分"}, "不允许覆盖总分"))
				return
			}
			if *in.TotalOverride < 0 || *in.TotalOverride > 100 {
				httpx.Fail(c, httpx.Validation(map[string]string{"total_override": "0~100"}, "总分超出范围"))
				return
			}
			total = *in.TotalOverride
			grade = rubric.Grade(total)
		}
		scoresJSON, _ = json.Marshal(in.Scores)
	} else {
		if len(reasons) == 0 {
			httpx.Fail(c, httpx.Validation(map[string]string{"exception_reasons": "请至少选择一项异常原因"}, "请至少选择一项异常原因"))
			return
		}
		if !s.validExceptionReasons(ctx, reasons) {
			httpx.Fail(c, httpx.Validation(map[string]string{"exception_reasons": "包含未知的异常原因"}, "异常原因不合法"))
			return
		}
		comment = strings.TrimSpace(in.ExceptionNote)
		scoresJSON = nil
	}

	snap, _ := json.Marshal(rubric.Snapshot())
	var submitUUID any
	if in.ClientSubmitID != "" {
		submitUUID = in.ClientSubmitID
	}

	var newVersion int
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var prev int
		_ = tx.QueryRow(ctx, `SELECT version FROM annotations WHERE image_id=$1`, id).Scan(&prev)
		newVersion = prev + 1

		var annID int64
		err := tx.QueryRow(ctx,
			`INSERT INTO annotations (image_id, annotator_id, version, kind, scores, total, grade, comment,
			        exception_reasons, exception_note, rubric_id, rubric_snapshot, duration_ms,
			        client_submit_id, submitted_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14,now(),now())
			 ON CONFLICT (image_id) DO UPDATE SET
			   annotator_id=EXCLUDED.annotator_id, version=EXCLUDED.version, kind=EXCLUDED.kind,
			   scores=EXCLUDED.scores, total=EXCLUDED.total, grade=EXCLUDED.grade,
			   comment=EXCLUDED.comment, exception_reasons=EXCLUDED.exception_reasons,
			   exception_note=EXCLUDED.exception_note, rubric_id=EXCLUDED.rubric_id,
			   rubric_snapshot=EXCLUDED.rubric_snapshot, duration_ms=EXCLUDED.duration_ms,
			   client_submit_id=EXCLUDED.client_submit_id, submitted_at=now(), updated_at=now()
			 RETURNING id`,
			id, me.ID, newVersion, kind, scoresJSON, total, grade, comment,
			reasons, in.ExceptionNote, rubric.ID, snap, in.DurationMS, submitUUID).Scan(&annID)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(gin.H{
			"kind": kind, "scores": in.Scores, "total": total, "grade": grade,
			"comment": comment, "exception_reasons": reasons, "exception_note": in.ExceptionNote,
		})
		if _, err := tx.Exec(ctx,
			`INSERT INTO annotation_versions (image_id, version, annotator_id, payload, submitted_at)
			 VALUES ($1,$2,$3,$4::jsonb,now()) ON CONFLICT (image_id, version) DO NOTHING`,
			id, newVersion, me.ID, payload); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE images SET status='submitted', submitted_at=now(), updated_at=now()
			  WHERE id=$1 AND annotator_id=$2 AND status IN ('assigned','rejected')`, id, me.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM drafts WHERE user_id=$1 AND image_id=$2`, me.ID, id); err != nil {
			return err
		}
		s.audit(c, "annotation.submit", "image", id, map[string]any{
			"kind": kind, "version": newVersion, "total": total,
		})
		return nil
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	op := "annotation.submit"
	if kind == "exception" {
		op = "annotation.exception"
	}
	s.audit(c, op, "image", id, map[string]any{"kind": kind, "version": newVersion, "total": total})
	httpx.OK(c, gin.H{
		"version": newVersion, "kind": kind, "total": total, "grade": grade,
		"status": "submitted", "comment_required": rubric.CommentMin,
	})
}

func (s *Server) validExceptionReasons(ctx context.Context, reasons []string) bool {
	raw, ok := s.settingValue(ctx, "exception_reasons").([]any)
	if !ok {
		return true
	}
	allowed := map[string]bool{}
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			if k, ok := m["key"].(string); ok {
				allowed[k] = true
			}
		}
	}
	for _, r := range reasons {
		if !allowed[r] {
			return false
		}
	}
	return true
}
