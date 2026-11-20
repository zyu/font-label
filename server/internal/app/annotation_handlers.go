package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

// client_submit_id 落库列是 uuid，格式不对要在入口挡住，不能让它变成 500。
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type submitInput struct {
	ClientSubmitID   string            `json:"client_submit_id"`
	Kind             string            `json:"kind"`
	Grades           map[string]string `json:"grades"`
	LegacyScores     json.RawMessage   `json:"scores"`
	DimComments      map[string]string `json:"dim_comments"`
	LegacyTotal      json.RawMessage   `json:"total_override"`
	ExceptionReasons []string          `json:"exception_reasons"`
	ExceptionNote    string            `json:"exception_note"`
	DurationMS       int               `json:"duration_ms"`
}

func (s *Server) handleSaveDraft(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	body, err := c.GetRawData()
	if err != nil || !json.Valid(body) {
		httpx.Fail(c, httpx.BadRequest("草稿内容不是合法 JSON"))
		return
	}
	var draft struct {
		Grades       map[string]string `json:"grades"`
		LegacyScores json.RawMessage   `json:"scores"`
		LegacyTotal  json.RawMessage   `json:"total_override"`
	}
	if err := json.Unmarshal(body, &draft); err != nil || len(draft.LegacyScores) > 0 || len(draft.LegacyTotal) > 0 {
		httpx.Fail(c, httpx.Validation(map[string]string{"grades": "等级只能是 S/A/B/C/D"}, "请保存字母等级草稿"))
		return
	}
	if len(draft.Grades) > 0 {
		rubric, err := s.activeRubric(ctx)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if err := rubric.ValidateGrades(draft.Grades, true); err != nil {
			httpx.Fail(c, httpx.Validation(map[string]string{"grades": err.Error()}, err.Error()))
			return
		}
	}
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var annotatorID int64
		var status string
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE(annotator_id,0), status, quality_pending FROM images WHERE id=$1 FOR UPDATE`, id).Scan(&annotatorID, &status, &pending); err != nil {
			return httpx.NotFound("图片不存在")
		}
		if annotatorID != me.ID {
			return httpx.Forbidden("这不是你的任务")
		}
		if status == "exception_locked" {
			return exceptionLockedError()
		}
		if status != "assigned" && status != "rejected" {
			return httpx.Conflict("该任务当前状态为只读，不能保存草稿")
		}
		if pending {
			return httpx.Conflict("该任务正在质检或仲裁")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO drafts (user_id, image_id, payload, updated_at) VALUES ($1,$2,$3::jsonb,now())
			 ON CONFLICT (user_id, image_id) DO UPDATE SET payload=EXCLUDED.payload, updated_at=now()`,
			me.ID, id, body); err != nil {
			return err
		}
		return s.insertAudit(c, tx, "annotation.draft", "image", id, nil)
	})
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
		`SELECT d.payload::text FROM drafts d JOIN images i ON i.id=d.image_id
		WHERE d.user_id=$1 AND d.image_id=$2 AND i.status<>'exception_locked'`, me.ID, id).Scan(&raw)
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
	if in.ClientSubmitID != "" && !uuidRe.MatchString(in.ClientSubmitID) {
		httpx.Fail(c, httpx.Validation(map[string]string{"client_submit_id": "需为 UUID，如 8f14e45f-ea4b-4c1e-9a63-2b0f0a1c7d33"}, "提交标识格式不正确"))
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
		var ownerID int64
		err := s.pool().QueryRow(ctx,
			`SELECT a.image_id, a.version, a.annotator_id FROM annotations a JOIN images i ON i.id=a.image_id WHERE a.client_submit_id=$1 AND a.version>i.voided_through_version`,
			in.ClientSubmitID).Scan(&imgID, &ver, &ownerID)
		if err == nil {
			if ownerID != me.ID {
				httpx.Fail(c, httpx.Forbidden("这不是你的任务"))
				return
			}
			if imgID != id {
				httpx.Fail(c, httpx.Conflict("提交标识已被其他任务使用"))
				return
			}
			httpx.OK(c, gin.H{"duplicate": true, "version": ver})
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
	kind := in.Kind
	if kind != "score" && kind != "exception" {
		httpx.Fail(c, httpx.Validation(map[string]string{"kind": "score 或 exception"}, "标注类型不合法"))
		return
	}

	var (
		comment        string
		reasons        = in.ExceptionReasons
		gradesJSON     []byte
		dimComments    = map[string]string{}
		dimCommentsRaw []byte
	)
	if reasons == nil {
		reasons = []string{}
	}
	if len(in.LegacyScores) > 0 || len(in.LegacyTotal) > 0 {
		httpx.Fail(c, httpx.Validation(map[string]string{"grades": "请提交 S/A/B/C/D 等级"}, "请通过 grades 提交五维等级"))
		return
	}
	if kind == "score" {
		if err := rubric.ValidateGrades(in.Grades, false); err != nil {
			httpx.Fail(c, httpx.Validation(map[string]string{"grades": err.Error()}, err.Error()))
			return
		}
		// 点评按维度分开写：五维各一条，每条形如「白平衡：…」，字数由 rubric 配置
		if err := rubric.ValidateDimComments(in.DimComments); err != nil {
			httpx.Fail(c, httpx.Validation(map[string]string{"dim_comments": err.Error()}, err.Error()))
			return
		}
		for k, v := range in.DimComments {
			dimComments[k] = strings.TrimSpace(v)
		}
		gradesJSON, _ = json.Marshal(in.Grades)
	} else {
		if len(in.Grades) > 0 || len(in.DimComments) > 0 {
			httpx.Fail(c, httpx.Validation(map[string]string{"kind": "异常输入不可包含定级或维度评语"}, "异常输入不可包含定级或维度评语"))
			return
		}
		if len(reasons) == 0 {
			httpx.Fail(c, httpx.Validation(map[string]string{"exception_reasons": "请至少选择一项异常原因"}, "请至少选择一项异常原因"))
			return
		}
		if !s.validExceptionReasons(ctx, reasons) {
			httpx.Fail(c, httpx.Validation(map[string]string{"exception_reasons": "包含未知的异常原因"}, "异常原因不合法"))
			return
		}
		comment = strings.TrimSpace(in.ExceptionNote)
		gradesJSON = nil
	}

	snap, _ := json.Marshal(rubric)
	dimCommentsRaw, _ = json.Marshal(dimComments)
	var submitUUID any
	if in.ClientSubmitID != "" {
		submitUUID = in.ClientSubmitID
	}

	op := "annotation.submit"
	if kind == "exception" {
		op = "annotation.exception"
	}
	var newVersion int
	var duplicate bool
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		// Serialize with review, draft saves and claim changes before mutating annotations.
		sourceID, err := lockTaskSource(ctx, tx, id)
		if err != nil {
			return err
		}
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT status, COALESCE(annotator_id,0), quality_pending FROM images WHERE id=$1 FOR UPDATE`, id).Scan(&status, &annotatorID, &pending); err != nil {
			return httpx.NotFound("图片不存在")
		}
		if annotatorID != me.ID {
			return httpx.Forbidden("这不是你的任务")
		}
		if status == "exception_locked" {
			return exceptionLockedError()
		}
		// A concurrent retry may have committed while this request waited for the lock.
		if in.ClientSubmitID != "" {
			var previousImageID int64
			var voided bool
			err := tx.QueryRow(ctx, `
				SELECT a.image_id,a.version,a.version<=i.voided_through_version
				FROM annotations a JOIN images i ON i.id=a.image_id WHERE a.client_submit_id=$1`,
				in.ClientSubmitID).Scan(&previousImageID, &newVersion, &voided)
			if err == nil {
				if previousImageID != id {
					return httpx.Conflict("提交标识已被其他任务使用")
				}
				if voided {
					return httpx.Conflict("这次提交已作废，请重新提交标注")
				}
				duplicate = true
				return nil
			}
			if err != pgx.ErrNoRows {
				return err
			}
		}
		if status != "assigned" && status != "rejected" {
			return httpx.Conflict("该任务当前状态为只读，不能提交")
		}
		if pending {
			return httpx.Conflict("该任务正在质检或仲裁")
		}
		if sourceID != 0 && kind == "score" {
			if err := checkGroupGrades(ctx, tx, sourceID, id, rubric, in.Grades, dimComments); err != nil {
				return err
			}
		}
		var prev int
		_ = tx.QueryRow(ctx, `SELECT version FROM annotations WHERE image_id=$1`, id).Scan(&prev)
		newVersion = prev + 1

		_, err = tx.Exec(ctx,
			`INSERT INTO annotations (image_id, annotator_id, version, kind, grades, comment,
			        dim_comments, exception_reasons, exception_note, rubric_id, rubric_snapshot, duration_ms,
			        client_submit_id, submitted_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7::jsonb,$8,$9,$10,$11::jsonb,$12,$13,now(),now())
			 ON CONFLICT (image_id) DO UPDATE SET
			   annotator_id=EXCLUDED.annotator_id, version=EXCLUDED.version, kind=EXCLUDED.kind,
			   grades=EXCLUDED.grades,
			   comment=EXCLUDED.comment, dim_comments=EXCLUDED.dim_comments,
			   exception_reasons=EXCLUDED.exception_reasons,
			   exception_note=EXCLUDED.exception_note, rubric_id=EXCLUDED.rubric_id,
			   rubric_snapshot=EXCLUDED.rubric_snapshot, duration_ms=EXCLUDED.duration_ms,
			   client_submit_id=EXCLUDED.client_submit_id, submitted_at=now(), updated_at=now()`,
			id, me.ID, newVersion, kind, gradesJSON, comment, dimCommentsRaw,
			reasons, comment, rubric.ID, snap, in.DurationMS, submitUUID)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(gin.H{
			"kind": kind, "grades": in.Grades, "dim_comments": dimComments,
			"exception_reasons": reasons, "exception_note": comment,
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
		return s.insertAudit(c, tx, op, "image", id, map[string]any{"kind": kind, "version": newVersion})
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if duplicate {
		httpx.OK(c, gin.H{"duplicate": true, "version": newVersion})
		return
	}
	httpx.OK(c, gin.H{
		"version": newVersion, "kind": kind,
		"status": "submitted", "comment_required": rubric.DimCommentMin,
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
