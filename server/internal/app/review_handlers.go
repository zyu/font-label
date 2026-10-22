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
	args := []any{me.ID}
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
		imageSelect+` WHERE `+clause+` ORDER BY i.submitted_at ASC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)),
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
		  WHERE r.reviewer_id = $1
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
		Action     string         `json:"action"`
		Comment    string         `json:"comment"`
		ReasonTags []string       `json:"reason_tags"`
		ScorePatch map[string]int `json:"score_patch"`
		Drawings   []any          `json:"drawings"`
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
		oldScores   string
	)
	err = s.pool().QueryRow(ctx,
		`SELECT i.status, COALESCE(i.annotator_id,0), COALESCE(a.kind,''), COALESCE(a.id,0),
		        COALESCE(a.version,0), COALESCE(a.scores::text,'{}')
		   FROM images i LEFT JOIN annotations a ON a.image_id=i.id WHERE i.id=$1`, id).
		Scan(&status, &annotatorID, &kind, &annID, &version, &oldScores)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
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
	var newTotal float64
	if len(in.ScorePatch) > 0 {
		if kind != "score" {
			httpx.Fail(c, httpx.Validation(map[string]string{"score_patch": "异常图不能改分"}, "异常图不能改分"))
			return
		}
		merged := map[string]int{}
		_ = json.Unmarshal([]byte(oldScores), &merged)
		for k, v := range in.ScorePatch {
			merged[k] = v
		}
		if err := rubric.ValidateScores(merged); err != nil {
			httpx.Fail(c, httpx.Validation(map[string]string{"score_patch": err.Error()}, err.Error()))
			return
		}
		newTotal, _ = rubric.Total(merged)
		patchJSON, _ = json.Marshal(in.ScorePatch)
	}

	var drawingsJSON []byte
	if len(in.Drawings) > 0 {
		drawingsJSON, _ = json.Marshal(in.Drawings)
	}

	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		tag, execErr := tx.Exec(ctx,
			`UPDATE images SET status=$3, reviewed_at=now(),
			        review_count = review_count + 1,
			        reject_count = reject_count + CASE WHEN $2='reject' THEN 1 ELSE 0 END,
			        updated_at=now()
			  WHERE id=$1 AND status='submitted' AND COALESCE(annotator_id,0) <> $4`,
			id, in.Action, newStatus, me.ID)
		if execErr != nil {
			return execErr
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("该图片已被其他人审核")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO reviews (image_id, annotation_id, annotation_version, reviewer_id,
			        action, comment, reason_tags, score_patch, drawings)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb)`,
			id, annID, version, me.ID, in.Action, in.Comment, in.ReasonTags, patchJSON, drawingsJSON); err != nil {
			return err
		}
		if len(in.ScorePatch) > 0 {
			grade := rubric.Grade(newTotal)
			if _, err := tx.Exec(ctx,
				`UPDATE annotations SET total=$2, grade=$3, updated_at=now() WHERE image_id=$1`,
				id, newTotal, grade); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	op := "review." + in.Action
	s.audit(c, op, "image", id, map[string]any{
		"comment": in.Comment, "tags": in.ReasonTags, "status": newStatus, "version": version,
	})
	httpx.OK(c, gin.H{"status": newStatus, "image_id": id, "total": newTotal})
}
