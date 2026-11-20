package app

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

func (s *Server) handleReviewQueue(c *gin.Context) {
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	page, size := httpx.Paging(c)

	clause := "i.status='submitted' AND COALESCE(i.annotator_id,0) <> $1"
	args := []any{me.ID, me.Role}
	clause += " AND " + reviewScope("$2", "$1")
	if fid := httpx.QueryInt64(c, "folder_id"); fid > 0 {
		args = append(args, fid)
		clause += " AND i.folder_id=$" + itoa(len(args))
	}
	if aid := httpx.QueryInt64(c, "annotator_id"); aid > 0 {
		args = append(args, aid)
		clause += " AND i.annotator_id=$" + itoa(len(args))
	}
	if kind := c.Query("kind"); kind == "score" || kind == "exception" {
		args = append(args, kind)
		clause += " AND a.kind=$" + itoa(len(args))
	}

	var total int
	if err := s.pool().QueryRow(ctx,
		`SELECT count(*) FROM images i LEFT JOIN annotations a ON a.image_id=i.id WHERE `+clause,
		args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool().Query(ctx,
		imageSelect+` WHERE `+clause+` ORDER BY i.submitted_at ASC, i.id ASC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)),
		args...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []ImageDTO{}
	for rows.Next() {
		r, err := scanImage(rows)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		items = append(items, s.toDTO(r))
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) handleMyReviews(c *gin.Context) {
	me := auth.MustCurrent(c)
	page, size := httpx.Paging(c)
	rows, err := s.pool().Query(c.Request.Context(),
		`SELECT r.id, i.stored_name, f.name, r.action, COALESCE(r.comment,''),
		        COALESCE(u.display_name,''), r.created_at, r.reason_tags
		   FROM reviews r
		   JOIN images i ON i.id = r.image_id
		   JOIN folders f ON f.id = i.folder_id
		   LEFT JOIN users u ON u.id = i.annotator_id
		  WHERE r.reviewer_id=$1 AND r.annotation_version>i.voided_through_version
		  ORDER BY r.created_at DESC LIMIT $2 OFFSET $3`, me.ID, size, (page-1)*size)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id        int64
			name, fld string
			action    string
			comment   string
			annotator string
			at        any
			tags      []string
		)
		if err := rows.Scan(&id, &name, &fld, &action, &comment, &annotator, &at, &tags); err != nil {
			httpx.Fail(c, err)
			return
		}
		items = append(items, gin.H{
			"id": id, "image": name, "folder": fld, "action": action,
			"comment": comment, "annotator": annotator, "created_at": at, "reason_tags": tags,
		})
	}
	httpx.OK(c, gin.H{"items": items, "page": page, "page_size": size})
}

func (s *Server) handleReviewReasons(c *gin.Context) {
	httpx.OK(c, gin.H{"items": s.settingValue(c.Request.Context(), "reject_reason_tags")})
}

func (s *Server) handleReview(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var in struct {
		Action      string            `json:"action"`
		Comment     string            `json:"comment"`
		ReasonTags  []string          `json:"reason_tags"`
		GradePatch  map[string]string `json:"grade_patch"`
		LegacyPatch json.RawMessage   `json:"score_patch"`
		Drawings    []any             `json:"drawings"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, httpx.BadRequest("请求体格式错误"))
		return
	}
	if in.Action != "approve" && in.Action != "reject" {
		httpx.Fail(c, httpx.Validation(map[string]string{"action": "approve 或 reject"}, "审核动作不合法"))
		return
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if in.Action == "reject" && utf8.RuneCountInString(in.Comment) < 5 {
		httpx.Fail(c, httpx.Validation(map[string]string{"comment": "批注至少 5 个字"}, "请填写批注"))
		return
	}

	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	if in.ReasonTags == nil {
		in.ReasonTags = []string{}
	}

	rubric, err := s.activeRubric(ctx)
	if err != nil {
		httpx.Fail(c, err)
		return
	}

	var (
		status      string
		annotatorID int64
		kind        string
		annID       int64
		version     int
		oldGrades   string
	)
	err = s.pool().QueryRow(ctx,
		`SELECT i.status, COALESCE(i.annotator_id,0), COALESCE(a.kind,''), COALESCE(a.id,0),
		        COALESCE(a.version,0), COALESCE(a.grades::text,'{}')
		   FROM images i LEFT JOIN annotations a ON a.image_id=i.id WHERE i.id=$1`, id).
		Scan(&status, &annotatorID, &kind, &annID, &version, &oldGrades)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if err := requireImageAccess(c, s.pool(), id, annotatorID); err != nil {
		httpx.Fail(c, err)
		return
	}
	if me.Role == "admin" {
		var grouped bool
		if err := s.pool().QueryRow(ctx, `SELECT group_id IS NOT NULL FROM images WHERE id=$1`, id).Scan(&grouped); err != nil {
			httpx.Fail(c, err)
			return
		}
		if grouped {
			httpx.Fail(c, httpx.Forbidden("分组任务只能由同组审核员审核"))
			return
		}
	}
	if annotatorID == me.ID {
		httpx.Fail(c, httpx.Forbidden("不能审核自己标注的图片"))
		return
	}
	if status != "submitted" {
		httpx.Fail(c, httpx.Conflict("该图片不在待审核状态"))
		return
	}

	newStatus := "approved"
	if in.Action == "reject" {
		newStatus = "rejected"
	} else if kind == "exception" {
		newStatus = "excluded"
	}

	var patchJSON []byte
	var mergedJSON []byte

	if len(in.LegacyPatch) > 0 {
		httpx.Fail(c, httpx.Validation(map[string]string{"grade_patch": "请提交字母等级"}, "请通过 grade_patch 修正等级"))
		return
	}
	if len(in.GradePatch) > 0 {
		if kind != "score" {
			httpx.Fail(c, httpx.Validation(map[string]string{"grade_patch": "异常图不能修正等级"}, "异常图不能修正等级"))
			return
		}
		merged := map[string]string{}
		_ = json.Unmarshal([]byte(oldGrades), &merged)
		for k, v := range in.GradePatch {
			merged[k] = v
		}
		if err := rubric.ValidateGrades(in.GradePatch, true); err != nil {
			httpx.Fail(c, httpx.Validation(map[string]string{"grade_patch": err.Error()}, err.Error()))
			return
		}
		patchJSON, _ = json.Marshal(in.GradePatch)
		mergedJSON, _ = json.Marshal(merged)
	}

	var drawingsJSON []byte
	if len(in.Drawings) > 0 {
		drawingsJSON, _ = json.Marshal(in.Drawings)
	}

	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(732140, 1)`); err != nil {
			return err
		}
		sourceID, err := lockTaskSource(ctx, tx, id)
		if err != nil {
			return err
		}
		var groupID int64
		execErr := tx.QueryRow(ctx,
			`UPDATE images i SET status=$3, reviewed_at=now(),
			        review_count = review_count + 1,
			        reject_count = reject_count + CASE WHEN $2='reject' THEN 1 ELSE 0 END,
			        updated_at=now()
			  WHERE id=$1 AND status='submitted' AND COALESCE(annotator_id,0) <> $4
			    AND `+reviewScope("$5", "$4")+`
			    AND EXISTS (SELECT 1 FROM users WHERE id=$4 AND role=$5 AND status='active')
			    AND EXISTS (SELECT 1 FROM annotations a WHERE a.image_id=i.id AND a.id=$6 AND a.version=$7)
			  RETURNING COALESCE(group_id,0)`,
			id, in.Action, newStatus, me.ID, me.Role, annID, version).Scan(&groupID)
		if execErr == pgx.ErrNoRows {
			return httpx.Conflict("任务状态或审核权限已变更")
		}
		if execErr != nil {
			return execErr
		}
		var reviewID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO reviews (image_id, annotation_id, annotation_version, reviewer_id,
			        action, comment, reason_tags, grade_patch, drawings)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb) RETURNING id`,
			id, annID, version, me.ID, in.Action, in.Comment, in.ReasonTags, patchJSON, drawingsJSON).Scan(&reviewID); err != nil {
			return err
		}
		if len(in.GradePatch) > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE annotations SET grades=$2::jsonb, updated_at=now() WHERE image_id=$1`,
				id, mergedJSON); err != nil {
				return err
			}
		}
		if groupID != 0 {
			if err := s.enqueueQualitySample(c, tx, id, reviewID, in.Action); err != nil {
				return err
			}
		}
		if sourceID != 0 && kind == "exception" {
			if _, err := tx.Exec(ctx, `SELECT sync_group_exception_locks($1)`, []int64{sourceID}); err != nil {
				return err
			}
		}
		return s.insertAudit(c, tx, "review."+in.Action, "image", id, map[string]any{
			"comment": in.Comment, "tags": in.ReasonTags, "status": newStatus, "version": version,
		})
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"status": newStatus, "image_id": id})
}
