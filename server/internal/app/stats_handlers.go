package app

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

// Both dashboards count review events from the same history. Attribute each
// event to the submitted version's author, even if the image is later reassigned.
// Legacy rows without a version snapshot fall back to the annotation's author.
const reviewStatsCTE = `review_events AS (
    SELECT r.image_id, r.action, r.annotation_version-i.voided_through_version AS attempt,
           COALESCE(v.annotator_id, a.annotator_id) AS annotator_id,
           row_number() OVER (PARTITION BY r.image_id ORDER BY r.annotation_version, r.id) AS review_order
      FROM reviews r
      JOIN annotations a ON a.id = r.annotation_id
      JOIN images i ON i.id=r.image_id AND r.annotation_version>i.voided_through_version
      LEFT JOIN annotation_versions v ON v.image_id = r.image_id AND v.version = r.annotation_version
), review_stats AS (
    SELECT annotator_id, count(*) AS review_events,
           count(*) FILTER (WHERE action='reject') AS reject_events,
           count(DISTINCT image_id) AS reviewed,
           count(DISTINCT image_id) FILTER (
               WHERE review_order=1 AND attempt=1 AND action='approve'
           ) AS first_pass
      FROM review_events GROUP BY annotator_id
)`

func (s *Server) handleStatsOverview(c *gin.Context) {
	ctx := c.Request.Context()
	var (
		unassigned, assigned, submitted, approved, rejected, excluded, total int
		todayUpload, todaySubmit, todayReview, todayReject                   int
		rejectRate                                                           float64
	)
	err := s.pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='unassigned'),
		       count(*) FILTER (WHERE status='assigned'),
		       count(*) FILTER (WHERE status='submitted' OR quality_pending),
		       count(*) FILTER (WHERE status='approved' AND NOT quality_pending),
		       count(*) FILTER (WHERE status='rejected' AND NOT quality_pending),
		       count(*) FILTER (WHERE status='excluded' AND NOT quality_pending),
		       count(*)
		  FROM images i WHERE i.status<>'exception_locked' AND `+workflowImageSQL).
		Scan(&unassigned, &assigned, &submitted, &approved, &rejected, &excluded, &total)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	_ = s.pool().QueryRow(ctx, `
		SELECT (SELECT count(*) FROM images WHERE group_id IS NULL AND created_at::date = current_date),
		       (SELECT count(*) FROM images WHERE submitted_at::date = current_date),
		       (SELECT count(*) FROM reviews r JOIN images i ON i.id=r.image_id AND r.annotation_version>i.voided_through_version WHERE r.created_at::date = current_date),
		       (SELECT count(*) FROM reviews r JOIN images i ON i.id=r.image_id AND r.annotation_version>i.voided_through_version WHERE r.created_at::date = current_date AND action='reject')`).
		Scan(&todayUpload, &todaySubmit, &todayReview, &todayReject)
	distribution, err := s.gradeDistribution(ctx, 0, 0)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var reviewEvents, rejectEvents int
	if err := s.pool().QueryRow(ctx, `WITH `+reviewStatsCTE+`
		SELECT COALESCE(sum(review_events),0)::bigint, COALESCE(sum(reject_events),0)::bigint
		FROM review_stats`).Scan(&reviewEvents, &rejectEvents); err != nil {
		httpx.Fail(c, err)
		return
	}
	if reviewEvents > 0 {
		rejectRate = float64(rejectEvents) / float64(reviewEvents) * 100
	}
	httpx.OK(c, gin.H{
		"total": total, "unassigned": unassigned, "assigned": assigned,
		"submitted": submitted, "approved": approved, "rejected": rejected, "excluded": excluded,
		"today_upload": todayUpload, "today_submit": todaySubmit,
		"today_review": todayReview, "today_reject": todayReject,
		"grade_distribution": distribution, "reject_rate": rejectRate,
		"review_events": reviewEvents, "reject_events": rejectEvents,
	})
}

func (s *Server) handleStatsAnnotators(c *gin.Context) {
	page, size := listPaging(c)
	var total int
	rows, err := s.pool().Query(c.Request.Context(), `WITH `+reviewStatsCTE+`, cl AS (
		    SELECT user_id, count(*) AS claim_times, COALESCE(sum(granted),0) AS granted
		      FROM claims GROUP BY user_id
		), an AS (
		    SELECT a.annotator_id, count(*) AS submitted,
		           COALESCE(avg(duration_ms) FILTER (WHERE duration_ms > 0),0) AS avg_ms,
		           count(*) FILTER (WHERE kind='exception') AS exceptions
		      FROM annotations a JOIN images i ON i.id=a.image_id AND a.version>i.voided_through_version GROUP BY a.annotator_id
		), grade_counts AS (
		    SELECT a.annotator_id, grade.value, count(*) AS n
		      FROM annotations a JOIN images i ON i.id=a.image_id AND a.version>i.voided_through_version
		      CROSS JOIN LATERAL jsonb_each_text(a.grades) grade
		     WHERE a.kind='score' GROUP BY a.annotator_id,grade.value
		), grade_stats AS (
		    SELECT annotator_id,jsonb_object_agg(value,n) AS levels FROM grade_counts GROUP BY annotator_id
		), im AS (
		    SELECT annotator_id,
		           count(*) FILTER (WHERE status='approved' AND NOT quality_pending) AS approved,
		           count(*) FILTER (WHERE status='rejected' AND NOT quality_pending) AS rejected,
		           count(*) FILTER (WHERE status='submitted' OR quality_pending) AS pending,
		           count(*) FILTER (WHERE status='assigned') AS holding
		      FROM images WHERE annotator_id IS NOT NULL GROUP BY annotator_id
		)
		SELECT u.id, u.display_name, u.status,
		       COALESCE(cl.claim_times,0)::int, COALESCE(cl.granted,0)::int,
		       COALESCE(an.submitted,0)::int, COALESCE(im.approved,0)::int,
		       COALESCE(im.rejected,0)::int, COALESCE(im.pending,0)::int,
		       COALESCE(im.holding,0)::int,
		       COALESCE(gs.levels,'{}'), COALESCE(an.avg_ms,0)::int,
		       COALESCE(an.exceptions,0)::int,
		       COALESCE(rs.first_pass,0)::int, COALESCE(rs.reviewed,0)::int,
		       COALESCE(rs.reject_events,0)::int, COALESCE(rs.review_events,0)::int, count(*) OVER ()
		  FROM users u
		  LEFT JOIN cl ON cl.user_id = u.id
		  LEFT JOIN an ON an.annotator_id = u.id
		  LEFT JOIN im ON im.annotator_id = u.id
		  LEFT JOIN grade_stats gs ON gs.annotator_id=u.id
		  LEFT JOIN review_stats rs ON rs.annotator_id = u.id
		 WHERE u.role = 'annotator' OR COALESCE(an.submitted,0) > 0 OR COALESCE(rs.review_events,0) > 0
		 ORDER BY COALESCE(an.submitted,0) DESC, u.id LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id                                   int64
			name, status                         string
			claimTimes, granted, submitted       int
			approved, rejected, pending, holding int
			distribution                         map[string]int
			avgMS, exceptions, firstPass         int
			reviewed, rejectEvents, reviewEvents int
		)
		if err := rows.Scan(&id, &name, &status, &claimTimes, &granted, &submitted, &approved,
			&rejected, &pending, &holding, &distribution, &avgMS, &exceptions,
			&firstPass, &reviewed, &rejectEvents, &reviewEvents, &total); err != nil {
			httpx.Fail(c, err)
			return
		}
		rejectRate := 0.0
		if reviewEvents > 0 {
			rejectRate = float64(rejectEvents) / float64(reviewEvents) * 100
		}
		firstPassRate := 0.0
		if reviewed > 0 {
			firstPassRate = float64(firstPass) / float64(reviewed) * 100
		}
		items = append(items, gin.H{
			"id": id, "name": name, "status": status,
			"claim_times": claimTimes, "granted": granted, "submitted": submitted,
			"approved": approved, "rejected": rejected, "pending": pending, "holding": holding,
			"grade_distribution": distribution, "avg_ms": avgMS, "exceptions": exceptions,
			"reject_rate": rejectRate, "first_pass_rate": firstPassRate,
			"reject_events": rejectEvents, "review_events": reviewEvents,
			"reviewed": reviewed, "first_pass": firstPass,
		})
	}
	if err := rows.Err(); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) handleStatsFolders(c *gin.Context) {
	s.listFolders(c, false, true)
}

func (s *Server) handleStatsRejections(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.pool().Query(ctx, `
		SELECT tag, count(*)::int
		  FROM reviews r JOIN images i ON i.id=r.image_id AND r.annotation_version>i.voided_through_version,
		       unnest(r.reason_tags) AS tag
		 WHERE r.action='reject'
		 GROUP BY tag ORDER BY count(*) DESC LIMIT 20`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	tags := []gin.H{}
	for rows.Next() {
		var tag string
		var n int
		if err := rows.Scan(&tag, &n); err != nil {
			httpx.Fail(c, err)
			return
		}
		tags = append(tags, gin.H{"tag": tag, "count": n})
	}

	byAnnotator := []gin.H{}
	rows2, err := s.pool().Query(ctx, `
		SELECT COALESCE(u.display_name,'未指派'), count(*)::int
		  FROM images i
		  LEFT JOIN users u ON u.id = i.annotator_id
		 WHERE i.reject_count > 0
		 GROUP BY u.display_name ORDER BY count(*) DESC LIMIT 20`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var name string
			var n int
			if err := rows2.Scan(&name, &n); err != nil {
				break
			}
			byAnnotator = append(byAnnotator, gin.H{"name": name, "count": n})
		}
	}
	httpx.OK(c, gin.H{"reasons": tags, "by_annotator": byAnnotator})
}

func (s *Server) handleStatsTimeseries(c *gin.Context) {
	days := httpx.QueryInt(c, "days", 14)
	if days < 1 || days > 90 {
		days = 14
	}
	rows, err := s.pool().Query(c.Request.Context(), `
		SELECT d::date,
		       (SELECT count(*) FROM images WHERE submitted_at::date = d::date),
		       (SELECT count(*) FROM images WHERE reviewed_at::date = d::date AND status IN ('approved','excluded') AND NOT quality_pending),
		       (SELECT count(*) FROM reviews r JOIN images i ON i.id=r.image_id AND r.annotation_version>i.voided_through_version WHERE r.created_at::date = d::date AND action='reject')
		  FROM generate_series(current_date - ($1::int - 1), current_date, interval '1 day') AS d
		 ORDER BY d`, days)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	points := []gin.H{}
	for rows.Next() {
		var d time.Time
		var submitted, approved, rejected int
		if err := rows.Scan(&d, &submitted, &approved, &rejected); err != nil {
			httpx.Fail(c, err)
			return
		}
		points = append(points, gin.H{
			"date": d.Format("2006-01-02"), "submitted": submitted,
			"approved": approved, "rejected": rejected,
		})
	}
	httpx.OK(c, gin.H{"points": points})
}

func (s *Server) handleStatsMe(c *gin.Context) {
	me := auth.MustCurrent(c)
	ctx := c.Request.Context()
	var (
		holding, rejected, approved, pending, submittedToday int
		granted                                              float64
	)
	err := s.pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='assigned'),
		       count(*) FILTER (WHERE status='rejected' AND NOT quality_pending),
		       count(*) FILTER (WHERE status='approved' AND NOT quality_pending),
		       count(*) FILTER (WHERE status='submitted' OR quality_pending),
		       count(*) FILTER (WHERE submitted_at::date = current_date)
		  FROM images WHERE annotator_id=$1`, me.ID).
		Scan(&holding, &rejected, &approved, &pending, &submittedToday)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	_ = s.pool().QueryRow(ctx,
		`SELECT COALESCE(sum(granted),0)::float8 FROM claims WHERE user_id=$1`, me.ID).Scan(&granted)
	annotatorID, reviewerID := me.ID, int64(0)
	if me.Role == "reviewer" {
		annotatorID, reviewerID = 0, me.ID
	}
	distribution, err := s.gradeDistribution(ctx, annotatorID, reviewerID)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var rejectEvents int
	if err := s.pool().QueryRow(ctx, `WITH `+reviewStatsCTE+`
		SELECT COALESCE((SELECT reject_events FROM review_stats WHERE annotator_id=$1),0)::int`, me.ID).Scan(&rejectEvents); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{
		"holding": holding, "rejected": rejected, "approved": approved,
		"pending": pending, "submitted_today": submittedToday,
		"granted": granted, "grade_distribution": distribution, "reject_events": rejectEvents,
	})
}

func (s *Server) gradeDistribution(ctx context.Context, annotatorID, reviewerID int64) (map[string]map[string]int, error) {
	rows, err := s.pool().Query(ctx, `
		SELECT grade.key, grade.value, count(*)
		FROM annotations a JOIN images i ON i.id=a.image_id AND a.version>i.voided_through_version
		CROSS JOIN LATERAL jsonb_each_text(a.grades) grade
		WHERE a.kind='score' AND ($1::bigint=0 OR a.annotator_id=$1)
		  AND ($2::bigint=0 OR EXISTS (
		      SELECT 1 FROM reviews r WHERE r.image_id=i.id AND r.annotation_version=a.version AND r.reviewer_id=$2))
		GROUP BY grade.key,grade.value`, annotatorID, reviewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]int{}
	for rows.Next() {
		var dimension, grade string
		var count int
		if err := rows.Scan(&dimension, &grade, &count); err != nil {
			return nil, err
		}
		if result[dimension] == nil {
			result[dimension] = map[string]int{}
		}
		result[dimension][grade] = count
	}
	return result, rows.Err()
}
