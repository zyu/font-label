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
	tag, err := s.pool().Exec(ctx, `
		WITH released AS (
			UPDATE images SET status='unassigned',annotator_id=NULL,claimed_at=NULL,lock_expires_at=NULL,updated_at=now()
			WHERE status='assigned' AND group_id IS NULL AND lock_expires_at < now()
			RETURNING id
		), released_claims AS (
			UPDATE claims SET released_at=now(),release_reason='timeout'
			WHERE released_at IS NULL AND image_ids && ARRAY(SELECT id FROM released)
		)
		INSERT INTO audit_logs(action,target_type,target_id)
		SELECT 'claim.timeout','image',id FROM released`)
	if err == nil && tag.RowsAffected() > 0 {
		slog.Warn("requeued expired claims", "count", tag.RowsAffected())
	}
	return err
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
		if tag, err := s.pool().Exec(ctx,
			`UPDATE export_jobs SET status='running' WHERE id=$1 AND status='pending'`, j.id); err != nil || tag.RowsAffected() == 0 {
			continue
		}
		if err := s.runExportJob(j.id, j.userID, j.format, []byte(j.filters)); err != nil {
			slog.Error("export job failed", "id", j.id, "err", err)
			s.failExportJob(ctx, j.id, err)
		}
	}
	return nil
}
