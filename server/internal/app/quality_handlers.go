package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

// The nth sample is picked when the accumulated quota crosses an integer.
// With a 5% rate this selects samples 20, 40, 60, and so on.
func qualitySampleOrder(number, rate int64) int64 {
	const scale = 10000
	if number <= 0 || rate <= 0 || rate > scale {
		return 0
	}
	quota := func(n int64) int64 {
		return (n/scale)*rate + (n%scale)*rate/scale
	}
	if order := quota(number); order > quota(number-1) {
		return order
	}
	return 0
}

func (s *Server) handleGetQualityPolicy(c *gin.Context) {
	var rate int
	var strata []string
	err := s.pool().QueryRow(c.Request.Context(), `SELECT sample_rate_bps,strata FROM quality_policy WHERE singleton`).Scan(&rate, &strata)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"sample_rate_bps": rate, "strata": strata})
}

func (s *Server) handleSetQualityPolicy(c *gin.Context) {
	var input struct {
		Rate   *int     `json:"sample_rate_bps"`
		Strata []string `json:"strata"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Rate == nil || *input.Rate < 0 || *input.Rate > 10000 {
		httpx.Fail(c, httpx.BadRequest("抽检比例须为 0% 至 100%，最多保留两位小数"))
		return
	}
	if input.Strata == nil {
		input.Strata = []string{}
	}
	for _, field := range input.Strata {
		if field != "group" && field != "folder" && field != "review" {
			httpx.Fail(c, httpx.BadRequest("抽检分层维度不合法"))
			return
		}
	}
	slices.Sort(input.Strata)
	input.Strata = slices.Compact(input.Strata)
	ctx := c.Request.Context()
	err := s.db.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE quality_policy SET sample_rate_bps=$1,strata=$2,
			revision=revision+CASE WHEN sample_rate_bps<>$1 OR strata<>$2 THEN 1 ELSE 0 END WHERE singleton`, *input.Rate, input.Strata); err != nil {
			return err
		}
		return s.insertAudit(c, tx, "quality.policy", "quality_policy", 0, map[string]any{"sample_rate_bps": *input.Rate, "strata": input.Strata})
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"sample_rate_bps": *input.Rate, "strata": input.Strata})
}

// Allocation uses the source sample's rank, independent of review timing.
const activeQualityUsersSQL = `SELECT array_agg(id ORDER BY id) AS ids FROM users WHERE role='quality' AND status='active'`

func (s *Server) enqueueQuality(c *gin.Context, tx pgx.Tx, imageID int64, version int, reviewID int64, action string) error {
	ctx := c.Request.Context()
	var gradeDispute bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM annotation_versions WHERE image_id=$1 AND version=$2 AND payload ? 'grade_differences'
	)`, imageID, version).Scan(&gradeDispute); err != nil {
		return err
	}
	if !gradeDispute {
		return s.enqueueQualitySample(c, tx, imageID, reviewID, action)
	}

	// Reuse the review-linked sample for arbitration without assigning an inspection.
	var sampleID int64
	if err := tx.QueryRow(ctx, `INSERT INTO quality_samples (review_id,image_id,sample_order)
		VALUES ($1,$2,1) RETURNING id`, reviewID, imageID).Scan(&sampleID); err != nil {
		return err
	}
	disputeID, arbiterID, err := registerDispute(ctx, tx, sampleID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE images SET quality_pending=true WHERE id=$1`, imageID); err != nil {
		return err
	}
	return s.insertAudit(c, tx, "quality.dispute", "quality_dispute", disputeID,
		map[string]any{"image_id": imageID, "review_id": reviewID, "arbiter_id": arbiterID, "reason": "grade_difference"})
}

func (s *Server) enqueueQualitySample(c *gin.Context, tx pgx.Tx, imageID, reviewID int64, action string) error {
	ctx := c.Request.Context()
	var number, rate, revision, groupID, folderID int64
	var strata []string
	if err := tx.QueryRow(ctx, `SELECT i.sample_no,p.sample_rate_bps,p.revision,i.group_id,i.folder_id,p.strata
		FROM images i CROSS JOIN quality_policy p WHERE i.id=$1 AND p.singleton FOR SHARE OF p`, imageID).
		Scan(&number, &rate, &revision, &groupID, &folderID, &strata); err != nil {
		return err
	}
	if rate > 0 && len(strata) > 0 {
		if !slices.Contains(strata, "group") {
			groupID = 0
		}
		if !slices.Contains(strata, "folder") {
			folderID = 0
		}
		if !slices.Contains(strata, "review") {
			action = ""
		}
		if err := tx.QueryRow(ctx, `INSERT INTO quality_strata (revision,group_key,folder_key,review_key)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (revision,group_key,folder_key,review_key)
			DO UPDATE SET last_no=quality_strata.last_no+1 RETURNING last_no`,
			revision, groupID, folderID, action).Scan(&number); err != nil {
			return err
		}
	}
	order := qualitySampleOrder(number, rate)
	if order == 0 {
		return nil
	}
	var sampleID int64
	var inspectorID *int64
	if err := tx.QueryRow(ctx, `INSERT INTO quality_samples (review_id,image_id,sample_order,inspector_id)
		SELECT $1,$2,$3,ids[1+(($3::bigint-1) % NULLIF(cardinality(ids),0))]
		FROM (`+activeQualityUsersSQL+`) active RETURNING id,inspector_id`, reviewID, imageID, order).Scan(&sampleID, &inspectorID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE images SET quality_pending=true WHERE id=$1`, imageID); err != nil {
		return err
	}
	return s.insertAudit(c, tx, "quality.sample", "quality_sample", sampleID, map[string]any{"image_id": imageID, "review_id": reviewID, "inspector_id": inspectorID})
}

func assignUnclaimedSamples(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `UPDATE quality_samples q SET inspector_id=ids[1+((q.sample_order-1) % cardinality(ids))]
		FROM (`+activeQualityUsersSQL+`) active
		WHERE q.inspector_id IS NULL AND q.result IS NULL AND cardinality(ids)>0
		  AND NOT EXISTS (SELECT 1 FROM quality_disputes d WHERE d.sample_id=q.id)
		  AND EXISTS (SELECT 1 FROM reviews r JOIN images i ON i.id=r.image_id
		              WHERE r.id=q.review_id AND r.action='approve' AND r.annotation_version>i.voided_through_version)`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT assign_pending_quality_disputes()`)
	return err
}

const qualityFrom = ` FROM quality_samples q
	JOIN reviews r ON r.id=q.review_id AND r.action='approve'
	JOIN images i ON i.id=q.image_id AND r.annotation_version>i.voided_through_version
	LEFT JOIN annotation_groups g ON g.id=i.group_id
	LEFT JOIN users inspector ON inspector.id=q.inspector_id
	LEFT JOIN quality_disputes d ON d.sample_id=q.id
	LEFT JOIN users arbiter ON arbiter.id=d.arbiter_id
	LEFT JOIN users reviewer ON reviewer.id=r.reviewer_id
	LEFT JOIN annotation_versions v ON v.image_id=i.id AND v.version=r.annotation_version
	LEFT JOIN users annotator ON annotator.id=v.annotator_id`

const qualityInspectionSQL = `(d.id IS NULL OR q.result IS NOT NULL)`

func (s *Server) handleListQualitySamples(c *gin.Context) { s.listQuality(c, false) }
func (s *Server) handleListDisputes(c *gin.Context)       { s.listQuality(c, true) }

func (s *Server) listQuality(c *gin.Context, disputes bool) {
	ctx := c.Request.Context()
	me := auth.MustCurrent(c)
	page, size := httpx.Paging(c)
	owner, result, order := "q.inspector_id", "q.result", "q.id"
	clause := " WHERE " + qualityInspectionSQL
	if disputes {
		clause = " WHERE d.id IS NOT NULL"
		owner, result, order = "d.arbiter_id", "d.result", "d.id"
	}
	args := []any{}
	if me.Role != "admin" {
		args = append(args, me.ID)
		clause += " AND " + owner + "=$1"
	}
	switch c.DefaultQuery("status", "pending") {
	case "pending":
		clause += " AND " + result + " IS NULL"
	case "done":
		clause += " AND " + result + " IS NOT NULL"
	case "all":
	default:
		httpx.Fail(c, httpx.BadRequest("status 不合法"))
		return
	}
	var total int
	if err := s.pool().QueryRow(ctx, "SELECT count(*)"+qualityFrom+clause, args...).Scan(&total); err != nil {
		httpx.Fail(c, err)
		return
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.pool().Query(ctx, `
		SELECT q.id,i.id,i.sample_no,i.stored_name,COALESCE(g.name,''), r.action, r.comment,r.annotation_version,
		       COALESCE(q.result,''),q.note,COALESCE(inspector.display_name,''),q.finished_at,
		       COALESCE(d.id,0),COALESCE(arbiter.display_name,''),COALESCE(d.result,''),COALESCE(d.note,''),
		       d.created_at,d.finished_at,COALESCE(v.payload-'grade_differences','{}'),r.grade_patch,
		       COALESCE(d.arbiter_id=q.inspector_id,false), (d.id IS NOT NULL AND d.arbiter_id IS NULL),
		       COALESCE(reviewer.display_name,''),COALESCE(annotator.display_name,''),
		       v.payload->'grade_differences'`+qualityFrom+clause+
		" ORDER BY "+order+" LIMIT $"+itoa(len(args)-1)+" OFFSET $"+itoa(len(args)), args...)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var sampleID, imageID, number, disputeID int64
		var name, group, review, reviewNote, qc, qcNote, inspector, arbiter, ruling, rulingNote, reviewer, annotator string
		var version int
		var selfArbitrated, awaitingArbiter bool
		var annotation, patch any
		var checkedAt, registeredAt, resolvedAt any
		var differences []gradeDifference
		if err := rows.Scan(&sampleID, &imageID, &number, &name, &group, &review, &reviewNote, &version,
			&qc, &qcNote, &inspector, &checkedAt, &disputeID, &arbiter, &ruling, &rulingNote,
			&registeredAt, &resolvedAt, &annotation, &patch, &selfArbitrated, &awaitingArbiter, &reviewer, &annotator, &differences); err != nil {
			httpx.Fail(c, err)
			return
		}
		item := gin.H{
			"id": sampleID, "image_id": imageID, "sample_id": fmt.Sprintf("P%06d", number),
			"image": name, "group": group, "original_url": s.signURL(imageID, "original"),
			"review": review, "review_note": reviewNote, "version": version, "annotation": annotation,
			"grade_patch": patch, "reviewer": reviewer, "annotator": annotator,
			"quality_result": qc, "quality_note": qcNote, "inspector": inspector, "checked_at": checkedAt,
			"dispute_id": disputeID, "arbiter": arbiter, "ruling": ruling, "ruling_note": rulingNote,
			"self_arbitrated": selfArbitrated, "awaiting_arbiter": awaitingArbiter,
			"registered_at": registeredAt, "resolved_at": resolvedAt,
		}
		if len(differences) > 0 {
			item["grade_differences"] = differences
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) handleCheckSample(c *gin.Context)    { s.decideQuality(c, false) }
func (s *Server) handleResolveDispute(c *gin.Context) { s.decideQuality(c, true) }

func (s *Server) decideQuality(c *gin.Context, dispute bool) {
	id, err := httpx.ParamInt64(c, "id")
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var input struct {
		Result string `json:"result"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || len(input.Note) > 8000 ||
		(dispute && input.Result != "return" && input.Result != "approve") ||
		(!dispute && input.Result != "pass" && input.Result != "fail") {
		httpx.Fail(c, httpx.BadRequest("请提交有效的结论和备注"))
		return
	}
	ctx := c.Request.Context()
	user := auth.MustCurrent(c)
	input.Note = strings.TrimSpace(input.Note)
	awaitingArbiter := false
	err = s.db.Tx(ctx, func(tx pgx.Tx) error {
		var imageID, sampleID, ownerID int64
		lookup := `SELECT image_id FROM quality_samples WHERE id=$1`
		if dispute {
			lookup = `SELECT q.image_id FROM quality_disputes d JOIN quality_samples q ON q.id=d.sample_id WHERE d.id=$1`
		}
		if err := tx.QueryRow(ctx, lookup, id).Scan(&imageID); errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("记录不存在")
		} else if err != nil {
			return err
		}
		sourceID, err := lockTaskSource(ctx, tx, imageID)
		if err != nil {
			return err
		}
		var previous string
		query := `SELECT q.image_id,q.id,COALESCE(q.inspector_id,0),COALESCE(q.result,'')` +
			qualityFrom + ` WHERE q.id=$1 AND ` + qualityInspectionSQL + ` FOR UPDATE OF i`
		if dispute {
			query = `SELECT q.image_id,q.id,COALESCE(d.arbiter_id,0),COALESCE(d.result,'')` +
				qualityFrom + ` WHERE d.id=$1 AND d.arbiter_id IS DISTINCT FROM q.inspector_id FOR UPDATE OF i`
		}
		err = tx.QueryRow(ctx, query, id).Scan(&imageID, &sampleID, &ownerID, &previous)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("记录不存在")
		}
		if err != nil {
			return err
		}
		if dispute && ownerID == 0 {
			return httpx.Conflict("暂无独立仲裁员，已挂起待分配")
		}
		if ownerID != user.ID {
			return httpx.Forbidden("该记录未分配给你")
		}
		if previous != "" {
			return httpx.Conflict("该记录已经处理")
		}
		detail := map[string]any{"image_id": imageID, "result": input.Result, "note": input.Note}
		if dispute {
			if err := s.resolveDispute(c, tx, id, imageID, input.Result, input.Note, detail); err != nil {
				return err
			}
			if sourceID != 0 {
				_, err := tx.Exec(ctx, `SELECT sync_group_exception_locks($1)`, []int64{sourceID})
				return err
			}
			return nil
		}
		tag, err := tx.Exec(ctx, `UPDATE quality_samples SET result=$2,note=$3,finished_at=now()
			WHERE id=$1 AND result IS NULL`, sampleID, input.Result, input.Note)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("该记录已经处理")
		}
		if input.Result == "fail" {
			disputeID, arbiterID, err := registerDispute(ctx, tx, sampleID)
			if err != nil {
				return err
			}
			detail["dispute_id"] = disputeID
			detail["arbiter_id"] = arbiterID
			awaitingArbiter = arbiterID == nil
		} else if _, err := tx.Exec(ctx, `UPDATE images SET quality_pending=false WHERE id=$1`, imageID); err != nil {
			return err
		}
		if sourceID != 0 && input.Result == "fail" {
			if _, err := tx.Exec(ctx, `SELECT sync_group_exception_locks($1)`, []int64{sourceID}); err != nil {
				return err
			}
		}
		return s.insertAudit(c, tx, "quality.check", "quality_sample", sampleID, detail)
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"ok": true, "awaiting_arbiter": awaitingArbiter})
}

func registerDispute(ctx context.Context, tx pgx.Tx, sampleID int64) (int64, *int64, error) {
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO quality_disputes (sample_id) VALUES ($1) RETURNING id`, sampleID).Scan(&id); err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT assign_pending_quality_disputes()`); err != nil {
		return 0, nil, err
	}
	var arbiter *int64
	err := tx.QueryRow(ctx, `SELECT arbiter_id FROM quality_disputes WHERE id=$1`, id).Scan(&arbiter)
	return id, arbiter, err
}

func (s *Server) resolveDispute(c *gin.Context, tx pgx.Tx, id, imageID int64, result, note string, detail map[string]any) error {
	ctx := c.Request.Context()
	tag, err := tx.Exec(ctx, `UPDATE quality_disputes SET result=$2,note=$3,finished_at=now()
		WHERE id=$1 AND result IS NULL`, id, result, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.Conflict("该记录已经处理")
	}
	if _, err := tx.Exec(ctx, `UPDATE images i SET quality_pending=false,updated_at=now(),reviewed_at=now(),
		status=CASE WHEN $2='return' THEN 'rejected' WHEN a.kind='exception' THEN 'excluded' ELSE 'approved' END,
		reject_count=reject_count+CASE WHEN $2='return' AND i.status<>'rejected' THEN 1 ELSE 0 END
		FROM annotations a WHERE i.id=$1 AND a.image_id=i.id`, imageID, result); err != nil {
		return err
	}
	return s.insertAudit(c, tx, "quality.arbitrate", "quality_dispute", id, detail)
}
