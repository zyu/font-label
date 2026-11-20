package app

import (
	"context"
	"log/slog"
	"time"
)

// StartWorker runs the background loops: lease expiry and async exports.
func (s *Server) StartWorker(ctx context.Context) {
	go s.loop(ctx, 60*time.Second, "requeue", s.requeueExpired)
	if s.oss != nil {
		go s.loop(ctx, 5*time.Second, "oss-exports", s.runPendingOSSExports)
	}
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
