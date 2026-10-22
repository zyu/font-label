package app

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/shangyue/font-label/server/internal/platform/auth"
	"github.com/shangyue/font-label/server/internal/platform/httpx"
)

func (s *Server) handleStatsOverview(c *gin.Context) {
	ctx := c.Request.Context()
	var (
		unassigned, assigned, submitted, approved, rejected, excluded, total int
		todayUpload, todaySubmit, todayReview, todayReject                  int
		avgTotal, rejectRate                                                float64
	)
	err := s.pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='unassigned'),
		       count(*) FILTER (WHERE status='assigned'),
		       count(*) FILTER (WHERE status='submitted'),
		       count(*) FILTER (WHERE status='approved'),
		       count(*) FILTER (WHERE status='rejected'),
		       count(*) FILTER (WHERE status='excluded'),
		       count(*)
		  FROM images`).
		Scan(&unassigned, &assigned, &submitted, &approved, &rejected, &excluded, &total)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	_ = s.pool().QueryRow(ctx, `
		SELECT (SELECT count(*) FROM images WHERE created_at::date = current_date),
		       (SELECT count(*) FROM images WHERE submitted_at::date = current_date),
		       (SELECT count(*) FROM reviews WHERE created_at::date = current_date),
		       (SELECT count(*) FROM reviews WHERE created_at::date = current_date AND action='reject')`).
		Scan(&todayUpload, &todaySubmit, &todayReview, &todayReject)
	_ = s.pool().QueryRow(ctx,
		`SELECT COALESCE(avg(total),0) FROM annotations WHERE kind='score' AND total IS NOT NULL`).Scan(&avgTotal)
	var reviewEvents, rejectEvents int
	_ = s.pool().QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE action='reject') FROM reviews`).Scan(&reviewEvents, &rejectEvents)
	if reviewEvents > 0 {
		rejectRate = float64(rejectEvents) / float64(reviewEvents) * 100
	}
	httpx.OK(c, gin.H{
		"total": total, "unassigned": unassigned, "assigned": assigned,
		"submitted": submitted, "approved": approved, "rejected": rejected, "excluded": excluded,
		"today_upload": todayUpload, "today_submit": todaySubmit,
		"today_review": todayReview, "today_reject": todayReject,
		"avg_total": avgTotal, "reject_rate": rejectRate,
	})
}

func (s *Server) handleStatsAnnotators(c *gin.Context) {
	rows, err := s.pool().Query(c.Request.Context(), `
		WITH cl AS (
		    SELECT user_id, count(*) AS claim_times, COALESCE(sum(granted),0) AS granted
		      FROM claims GROUP BY user_id
		), an AS (
		    SELECT annotator_id, count(*) AS submitted,
		           COALESCE(avg(total) FILTER (WHERE kind='score'),0) AS avg_total,
		           COALESCE(avg(duration_ms) FILTER (WHERE duration_ms > 0),0) AS avg_ms,
		           count(*) FILTER (WHERE kind='exception') AS exceptions
		      FROM annotations GROUP BY annotator_id
		), im AS (
		    SELECT annotator_id,
		           count(*) FILTER (WHERE status='approved') AS approved,
		           count(*) FILTER (WHERE status='rejected') AS rejected,
		           count(*) FILTER (WHERE status='submitted') AS pending,
		           count(*) FILTER (WHERE status='assigned') AS holding,
		           COALESCE(sum(reject_count),0) AS reject_events,
		           count(*) FILTER (WHERE status='approved' AND reject_count=0) AS first_pass,
		           count(*) FILTER (WHERE status IN ('approved','excluded','rejected')) AS reviewed
		      FROM images WHERE annotator_id IS NOT NULL GROUP BY annotator_id
		)
		SELECT u.id, u.display_name, u.status,
		       COALESCE(cl.claim_times,0)::int, COALESCE(cl.granted,0)::int,
		       COALESCE(an.submitted,0)::int, COALESCE(im.approved,0)::int,
		       COALESCE(im.rejected,0)::int, COALESCE(im.pending,0)::int,
		       COALESCE(im.holding,0)::int,
		       COALESCE(an.avg_total,0)::float8, COALESCE(an.avg_ms,0)::int,
		       COALESCE(an.exceptions,0)::int,
		       COALESCE(im.first_pass,0)::int, COALESCE(im.reviewed,0)::int,
		       COALESCE(im.reject_events,0)::int
		  FROM users u
		  LEFT JOIN cl ON cl.user_id = u.id
		  LEFT JOIN an ON an.annotator_id = u.id
		  LEFT JOIN im ON im.annotator_id = u.id
		 WHERE u.role = 'annotator' OR COALESCE(an.submitted,0) > 0
		 ORDER BY COALESCE(an.submitted,0) DESC, u.id`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var (
			id                                    int64
			name, status                          string
			claimTimes, granted, submitted        int
			approved, rejected, pending, holding  int
			avgTotal                              float64
			avgMS, exceptions, firstPass          int
			reviewed, rejectEvents                int
		)
		if err := rows.Scan(&id, &name, &status, &claimTimes, &granted, &submitted, &approved,
			&rejected, &pending, &holding, &avgTotal, &avgMS, &exceptions,
			&firstPass, &reviewed, &rejectEvents); err != nil {
			httpx.Fail(c, err)
			return
		}
		rejectRate := 0.0
		if reviewed > 0 {
			rejectRate = float64(rejected) / float64(reviewed) * 100
		}
		firstPassRate := 0.0
		if reviewed > 0 {
			firstPassRate = float64(firstPass) / float64(reviewed) * 100
		}
		items = append(items, gin.H{
			"id": id, "name": name, "status": status,
			"claim_times": claimTimes, "granted": granted, "submitted": submitted,
			"approved": approved, "rejected": rejected, "pending": pending, "holding": holding,
			"avg_total": avgTotal, "avg_ms": avgMS, "exceptions": exceptions,
			"reject_rate": rejectRate, "first_pass_rate": firstPassRate,
			"reject_events": rejectEvents, "reviewed": reviewed,
		})
	}
	httpx.OK(c, gin.H{"items": items})
}

func (s *Server) handleStatsFolders(c *gin.Context) {
	rows, err := s.pool().Query(c.Request.Context(),
		folderSelect+` WHERE f.archived_at IS NULL GROUP BY f.id, u.display_name ORDER BY f.id DESC`)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	defer rows.Close()
	items, err := scanFolders(rows)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

func (s *Server) handleStatsRejections(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := s.pool().Query(ctx, `
		SELECT tag, count(*)::int
		  FROM reviews r, unnest(r.reason_tags) AS tag
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
		       (SELECT count(*) FROM images WHERE reviewed_at::date = d::date AND status IN ('approved','excluded')),
		       (SELECT count(*) FROM reviews WHERE created_at::date = d::date AND action='reject')
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
		granted, avgTotal                                    float64
	)
	err := s.pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='assigned'),
		       count(*) FILTER (WHERE status='rejected'),
		       count(*) FILTER (WHERE status='approved'),
		       count(*) FILTER (WHERE status='submitted'),
		       count(*) FILTER (WHERE submitted_at::date = current_date)
		  FROM images WHERE annotator_id=$1`, me.ID).
		Scan(&holding, &rejected, &approved, &pending, &submittedToday)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	_ = s.pool().QueryRow(ctx,
		`SELECT COALESCE(sum(granted),0)::float8 FROM claims WHERE user_id=$1`, me.ID).Scan(&granted)
	_ = s.pool().QueryRow(ctx,
		`SELECT COALESCE(avg(total),0) FROM annotations WHERE annotator_id=$1 AND kind='score'`,
		me.ID).Scan(&avgTotal)
	var rejectEvents int
	_ = s.pool().QueryRow(ctx,
		`SELECT COALESCE(sum(reject_count),0)::int FROM images WHERE annotator_id=$1`, me.ID).Scan(&rejectEvents)
	httpx.OK(c, gin.H{
		"holding": holding, "rejected": rejected, "approved": approved,
		"pending": pending, "submitted_today": submittedToday,
		"granted": granted, "avg_total": avgTotal, "reject_events": rejectEvents,
	})
}
