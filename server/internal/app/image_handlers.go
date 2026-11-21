package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

func (s *Server) handleListImages(c *gin.Context) {
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	page, size := httpx.Paging(c)

	clause := "i.id > 0"
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		clause += fmt.Sprintf(cond, len(args))
	}

	switch me.Role {
	case "annotator":
		add(" AND i.annotator_id=$%d", me.ID)
	case "reviewer":
		args = append(args, me.Role, me.ID)
		clause += " AND " + reviewScope("$1", "$2")
	case "admin":
		if c.Query("sources") == "1" {
			clause += " AND i.group_id IS NULL"
		} else {
			clause += " AND " + workflowImageSQL
		}
	default:
		httpx.Fail(c, httpx.Forbidden("请在质检或仲裁队列查看任务"))
		return
	}
	if aid := httpx.QueryInt64(c, "annotator_id"); aid > 0 {
		add(" AND i.annotator_id=$%d", aid)
	}
	if fid := httpx.QueryInt64(c, "folder_id"); fid > 0 {
		add(" AND i.folder_id=$%d", fid)
	}
	if st := strings.TrimSpace(c.Query("status")); st != "" {
		parts := strings.Split(st, ",")
		add(" AND i.status = ANY($%d)", parts)
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		args = append(args, "%"+q+"%")
		n := len(args)
		clause += fmt.Sprintf(" AND (i.stored_name ILIKE $%d OR i.orig_name ILIKE $%d 			OR ('P'||lpad(i.sample_no::text,greatest(6,length(i.sample_no::text)),'0')) ILIKE $%d)", n, n, n)
	}
	if from := c.Query("from"); from != "" {
		add(" AND i.created_at >= $%d::date", from)
	}
	if to := c.Query("to"); to != "" {
		add(" AND i.created_at < $%d::date + 1", to)
	}

	var total int
	if err := s.pool().QueryRow(ctx, `SELECT count(*) FROM images i WHERE `+clause, args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool().Query(ctx,
		imageSelect+` WHERE `+clause+` ORDER BY i.id DESC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)),
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

func (s *Server) handleGetImage(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	tx, err := s.pool().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer tx.Rollback(ctx)
	row, err := scanImage(tx.QueryRow(ctx, imageSelectByID, id))
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if err := requireImageAccess(c, tx, id, row.AnnotatorID); err != nil {
		httpx.Fail(c, err)
		return
	}
	dto := s.toDTO(row)
	if reviewID := httpx.QueryInt64(c, "review_id"); reviewID > 0 {
		s.handleReviewedImage(c, tx, dto, reviewID)
		return
	}
	var draft any
	var raw string
	if row.Status == "assigned" || row.Status == "rejected" {
		if err := tx.QueryRow(ctx,
			`SELECT payload::text FROM drafts WHERE user_id=$1 AND image_id=$2`, auth.MustCurrent(c).ID, id).Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &draft)
		}
	}
	reviewImageID, reviewVersion := id, row.AnnotationVersion
	if row.ExceptionLockID != 0 {
		reviewImageID, reviewVersion = row.ExceptionLockID, row.AnnotationVersion
	}
	reviews := []ReviewDTO{}
	changes := map[string]gradeChange{}
	if reviewVersion > 0 {
		reviews, err = reviewsOf(ctx, tx, reviewImageID, reviewVersion)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if row.ExceptionLockID == 0 && dto.Kind == "score" {
			var submitted map[string]string
			err := tx.QueryRow(ctx, `SELECT payload->'grades' FROM annotation_versions WHERE image_id=$1 AND version=$2`, id, reviewVersion).Scan(&submitted)
			if err != nil && err != pgx.ErrNoRows {
				httpx.Fail(c, err)
				return
			}
			for key, now := range dto.Grades {
				if old := submitted[key]; old != "" && old != now {
					changes[key] = gradeChange{From: old, To: now}
				}
			}
		}
	}
	httpx.OK(c, gin.H{
		"image":         dto,
		"reviews":       reviews,
		"draft":         draft,
		"grade_changes": changes,
	})
}

type gradeChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleReviewedImage(c *gin.Context, tx pgx.Tx, image ImageDTO, reviewID int64) {
	me := auth.MustCurrent(c)
	if me.Role != "reviewer" && me.Role != "admin" {
		httpx.Fail(c, httpx.Forbidden("只有审核员可查看审核快照"))
		return
	}
	type snapshot struct {
		Kind     string            `json:"kind"`
		Grades   map[string]string `json:"grades"`
		Comments map[string]string `json:"dim_comments"`
		Reasons  []string          `json:"exception_reasons"`
		Note     string            `json:"exception_note"`
	}
	var payload snapshot
	var patch map[string]string
	var review ReviewDTO
	var submittedAt time.Time
	err := tx.QueryRow(c.Request.Context(), `
		SELECT COALESCE(v.payload,jsonb_build_object('kind',a.kind,'grades',a.grades,'dim_comments',a.dim_comments,
			'exception_reasons',a.exception_reasons,'exception_note',a.comment)),
		       r.id,r.action,r.comment,r.reason_tags,u.display_name,r.created_at,r.annotation_version,r.grade_patch,
		       COALESCE(v.submitted_at,a.submitted_at)
		FROM reviews r JOIN images i ON i.id=r.image_id
		JOIN users u ON u.id=r.reviewer_id
		LEFT JOIN annotation_versions v ON v.image_id=i.id AND v.version=r.annotation_version
		LEFT JOIN annotations a ON a.id=r.annotation_id AND a.version=r.annotation_version
		WHERE r.id=$1 AND i.id=$2 AND r.reviewer_id=$3 AND r.annotation_version>i.voided_through_version
		  AND (v.image_id IS NOT NULL OR a.id IS NOT NULL)`, reviewID, image.ID, me.ID).
		Scan(&payload, &review.ID, &review.Action, &review.Comment, &review.ReasonTags, &review.Reviewer, &review.CreatedAt,
			&review.Version, &patch, &submittedAt)
	if err == pgx.ErrNoRows {
		httpx.Fail(c, httpx.NotFound("审核记录不存在"))
		return
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	review.Source = "review"
	review.GradePatch = patch
	image.Kind, image.Comment = payload.Kind, payload.Note
	image.SubmittedAt = &submittedAt
	image.QualityPending = false
	image.Grades, image.DimComments, image.Exceptions = payload.Grades, payload.Comments, payload.Reasons
	if image.Grades == nil {
		image.Grades = map[string]string{}
	}
	maps.Copy(image.Grades, patch)
	image.Status = "approved"
	if review.Action == "reject" {
		image.Status = "rejected"
	} else if image.Kind == "exception" {
		image.Status = "excluded"
	}
	httpx.OK(c, gin.H{"image": image, "reviews": []ReviewDTO{review}, "draft": nil})
}

type imageQueries interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reviewsOf(ctx context.Context, db imageQueries, imageID int64, version int) ([]ReviewDTO, error) {
	rows, err := db.Query(ctx,
		`SELECT r.id,r.action,r.comment,r.reason_tags,COALESCE(u.display_name,''),
		        COALESCE(r.grade_patch::text,''),r.created_at,r.annotation_version,'review' AS source
		   FROM reviews r JOIN images i ON i.id=r.image_id
		   LEFT JOIN users u ON u.id=r.reviewer_id
		  WHERE r.image_id=$1 AND r.annotation_version>i.voided_through_version
		    AND ($2=0 OR r.annotation_version=$2)
		  UNION ALL
		 SELECT -d.id,CASE WHEN d.result='return' THEN 'reject' ELSE 'approve' END,
		        d.note,'{}',
		        COALESCE(u.display_name,''),'',d.finished_at,r.annotation_version,'arbitration'
		   FROM quality_disputes d JOIN quality_samples q ON q.id=d.sample_id
		   JOIN reviews r ON r.id=q.review_id JOIN images i ON i.id=q.image_id
		   LEFT JOIN users u ON u.id=d.arbiter_id
		  WHERE q.image_id=$1 AND d.result IS NOT NULL AND r.annotation_version>i.voided_through_version
		    AND ($2=0 OR r.annotation_version=$2)
		  UNION ALL
		 SELECT -q.id,CASE WHEN q.result='fail' THEN 'reject' ELSE 'approve' END,
		        q.note,'{}',COALESCE(u.display_name,''),'',q.finished_at,r.annotation_version,'quality'
		   FROM quality_samples q JOIN reviews r ON r.id=q.review_id AND r.action='approve'
		   JOIN images i ON i.id=q.image_id LEFT JOIN users u ON u.id=q.inspector_id
		  WHERE q.image_id=$1 AND q.result IS NOT NULL AND r.annotation_version>i.voided_through_version
		    AND ($2=0 OR r.annotation_version=$2)
		  ORDER BY created_at DESC, source ASC, id DESC`, imageID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewDTO{}
	for rows.Next() {
		var rv ReviewDTO
		var patch string
		if err := rows.Scan(&rv.ID, &rv.Action, &rv.Comment, &rv.ReasonTags, &rv.Reviewer,
			&patch, &rv.CreatedAt, &rv.Version, &rv.Source); err != nil {
			return nil, err
		}
		if patch != "" && patch != "null" {
			var v any
			_ = json.Unmarshal([]byte(patch), &v)
			rv.GradePatch = v
		}
		if rv.ReasonTags == nil {
			rv.ReasonTags = []string{}
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

func (s *Server) handleHistory(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	row, err := s.imageByID(ctx, id)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if err := requireImageAccess(c, s.pool(), id, row.AnnotatorID); err != nil {
		httpx.Fail(c, err)
		return
	}
	rows, err := s.pool().Query(ctx,
		`SELECT v.version, COALESCE(u.display_name,''), v.payload::text, v.submitted_at
		   FROM annotation_versions v
		   JOIN images i ON i.id=v.image_id AND v.version>i.voided_through_version
		   LEFT JOIN users u ON u.id = v.annotator_id
		  WHERE v.image_id=$1 ORDER BY v.version DESC`, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	versions := []gin.H{}
	for rows.Next() {
		var (
			ver      int
			who, raw string
			at       time.Time
		)
		if err := rows.Scan(&ver, &who, &raw, &at); err != nil {
			httpx.Fail(c, err)
			return
		}
		var payload any
		_ = json.Unmarshal([]byte(raw), &payload)
		versions = append(versions, gin.H{"version": ver, "annotator": who, "payload": payload, "submitted_at": at})
	}
	reviews, err := reviewsOf(ctx, s.pool(), id, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"versions": versions, "reviews": reviews})
}

func (s *Server) handleDeleteImage(c *gin.Context) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	var status, relPath, thumb, preview string
	err = s.pool().QueryRow(ctx,
		`SELECT status, rel_path, COALESCE(thumb_path,''), COALESCE(preview_path,'') FROM images WHERE id=$1`, id).
		Scan(&status, &relPath, &thumb, &preview)
	if err != nil {
		httpx.Fail(c, httpx.NotFound("图片不存在"))
		return
	}
	if status != "unassigned" {
		httpx.Fail(c, httpx.Conflict("图片已进入标注流程，不能删除"))
		return
	}
	tag, err := s.pool().Exec(ctx, `DELETE FROM images i WHERE i.id=$1 AND i.group_id IS NULL
		AND i.status='unassigned' AND `+undispatchedSQL, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Fail(c, httpx.Conflict("图片已进入标注流程，不能删除"))
		return
	}
	_ = s.store.Remove(relPath)
	_ = s.store.Remove(thumb)
	_ = s.store.Remove(preview)
	s.audit(c, "image.delete", "image", id, nil)
	httpx.OK(c, gin.H{"ok": true})
}

func requireImageAccess(c *gin.Context, db imageQueries, id, annotatorID int64) error {
	me := auth.MustCurrent(c)
	switch me.Role {
	case "admin":
		return nil
	case "annotator":
		if me.ID == annotatorID {
			return nil
		}
	case "reviewer":
		var allowed bool
		err := db.QueryRow(c.Request.Context(), `SELECT `+reviewScope("$2", "$3")+` FROM images i WHERE i.id=$1`,
			id, me.Role, me.ID).Scan(&allowed)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
	}
	return httpx.Forbidden("你没有权限查看该任务")
}
