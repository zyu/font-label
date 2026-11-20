-- +goose Up
ALTER TABLE images DROP CONSTRAINT images_status_check;
ALTER TABLE images ADD CONSTRAINT images_status_check CHECK (
    status IN ('unassigned','assigned','submitted','approved','rejected','excluded','exception_locked')
);
ALTER TABLE images ADD COLUMN exception_lock_id BIGINT REFERENCES images(id);
-- Keep the submitted versions and audit history while retiring them from the workflow.
ALTER TABLE images ADD COLUMN voided_through_version INT NOT NULL DEFAULT 0;
ALTER TABLE images ADD CONSTRAINT images_exception_lock_check CHECK (
    (status='exception_locked') = (exception_lock_id IS NOT NULL)
    AND (exception_lock_id IS NULL OR (source_image_id IS NOT NULL AND exception_lock_id <> id))
);

-- The caller holds the source image locks before changing any group task.
-- +goose StatementBegin
CREATE FUNCTION sync_group_exception_locks(source_ids BIGINT[]) RETURNS void LANGUAGE sql AS $$
    WITH confirmed AS (
        SELECT DISTINCT ON (i.source_image_id) i.source_image_id, i.id
        FROM images i JOIN annotations a ON a.image_id=i.id AND a.version>i.voided_through_version
        WHERE i.source_image_id IS NOT NULL
          AND (source_ids IS NULL OR i.source_image_id=ANY(source_ids))
          AND i.status='excluded' AND a.kind='exception'
          AND NOT EXISTS (
              SELECT 1 FROM quality_samples q
              JOIN reviews r ON r.id=q.review_id AND r.annotation_version=a.version
              LEFT JOIN quality_disputes d ON d.sample_id=q.id
              WHERE q.image_id=i.id AND q.result='fail' AND d.result IS DISTINCT FROM 'approve'
          )
        ORDER BY i.source_image_id, i.reviewed_at NULLS LAST, i.id
    ), targets AS (
        SELECT i.id, c.id AS lock_id, COALESCE(a.version, i.voided_through_version) AS version
        FROM images i
        LEFT JOIN confirmed c ON c.source_image_id=i.source_image_id AND c.id<>i.id
        LEFT JOIN annotations a ON a.image_id=i.id
        WHERE i.source_image_id IS NOT NULL
          AND (source_ids IS NULL OR i.source_image_id=ANY(source_ids))
          AND i.exception_lock_id IS DISTINCT FROM c.id
    )
    UPDATE images i SET
        status=CASE WHEN t.lock_id IS NULL THEN 'assigned' ELSE 'exception_locked' END,
        exception_lock_id=t.lock_id, voided_through_version=t.version,
        quality_pending=false, submitted_at=NULL, reviewed_at=NULL,
        review_count=0, reject_count=0, updated_at=now()
    FROM targets t WHERE i.id=t.id;
$$;
-- +goose StatementEnd

SELECT sync_group_exception_locks(NULL);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'group alignment migration is irreversible because it voids superseded submissions';
END $$;
-- +goose StatementEnd
