package app

import (
	"context"
	"log/slog"
	"time"
)

func contextBackground() context.Context { return context.Background() }

// StartWorker runs the background loops: lease expiry and async exports.
func (s *Server) StartWorker(ctx context.Context) {
	go s.loop(ctx, 60*time.Second, "requeue", s.requeueExpired)
	go s.loop(ctx, 5*time.Second, "exports", s.runPendingExports)
}

func (s *Server) loop(ctx context.Context, every time.Duration, name string, fn func(context.Context) error) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := fn(ctx); err != nil {
				slog.Error("worker task failed", "task", name, "err", err)
			}
		}
	}
}

// requeueExpired releases claimed-but-unsubmitted images whose lease expired.
func (s *Server) requeueExpired(ctx context.Context) error {
	rows, err := s.pool().Query(ctx, `
		UPDATE images
		   SET status='unassigned', annotator_id=NULL, claimed_at=NULL,
		       lock_expires_at=NULL, updated_at=now()
		 WHERE status='assigned' AND lock_expires_at IS NOT NULL AND lock_expires_at < now()
		 RETURNING id, COALESCE(annotator_id,0)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	released := 0
	owners := map[int64]int{}
	for rows.Next() {
		var id, owner int64
		if err := rows.Scan(&id, &owner); err != nil {
			return err
		}
		released++
		owners[owner]++
	}
	if released > 0 {
		slog.Warn("requeued expired claims", "count", released)
		_, _ = s.pool().Exec(ctx,
			`INSERT INTO audit_logs (actor_id, action, target_type, target_id, detail)
			 VALUES (NULL,'claim.timeout','image',NULL,$1::jsonb)`,
			[]byte(`{"count":`+itoa(released)+`}`))
		_, _ = s.pool().Exec(ctx,
			`UPDATE claims SET released_at=now(), release_reason='timeout'
			  WHERE released_at IS NULL AND created_at < now() - interval '1 hour'`)
	}
	return rows.Err()
}

func (s *Server) runPendingExports(ctx context.Context) error {
	rows, err := s.pool().Query(ctx,
		`SELECT id, user_id, format, filters::text FROM export_jobs
		  WHERE status='pending' ORDER BY id LIMIT 3`)
	if err != nil {
		return err
	}
	type job struct {
		id      int64
		userID  int64
		format  string
		filters string
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.userID, &j.format, &j.filters); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		if _, err := s.pool().Exec(ctx,
			`UPDATE export_jobs SET status='running' WHERE id=$1`, j.id); err != nil {
			continue
		}
		if err := s.runExportJob(j.id, j.userID, j.format, []byte(j.filters)); err != nil {
			slog.Error("export job failed", "id", j.id, "err", err)
			s.failExportJob(ctx, j.id, err)
		}
	}
	return nil
}
