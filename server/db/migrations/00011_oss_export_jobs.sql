-- +goose Up
ALTER TABLE export_jobs
    ADD COLUMN destination TEXT NOT NULL DEFAULT 'download'
        CHECK (destination IN ('download','oss')),
    ADD COLUMN object_key TEXT,
    ADD COLUMN object_url TEXT,
    ADD CONSTRAINT export_jobs_oss_object_required CHECK (
        destination <> 'oss' OR (object_key IS NOT NULL AND object_url IS NOT NULL)
    );

CREATE INDEX idx_export_jobs_oss_queue ON export_jobs (id)
    WHERE destination='oss' AND status IN ('pending','running');

-- +goose Down
-- Keep OSS history distinguishable from downloadable jobs if rolled back.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM export_jobs WHERE destination='oss') THEN
        RAISE EXCEPTION 'cannot remove OSS export metadata while OSS jobs exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX idx_export_jobs_oss_queue;
ALTER TABLE export_jobs DROP CONSTRAINT export_jobs_oss_object_required,
    DROP COLUMN object_url, DROP COLUMN object_key, DROP COLUMN destination;
