-- +goose Up
-- Migrate the former default, preserving deliberate overrides including zero.
UPDATE settings SET value='10'::jsonb, updated_at=now()
 WHERE key='max_file_mb' AND value='20'::jsonb;

-- Restore access to unfinished work hidden by the former unrestricted archive.
UPDATE folders SET archived_at=NULL, updated_at=now()
 WHERE archived_at IS NOT NULL AND EXISTS (
   SELECT 1 FROM images WHERE folder_id=folders.id AND status NOT IN ('approved','excluded')
 );

-- +goose Down
-- Data repairs are intentionally retained: previous archive state and whether a
-- 10MB value was customized cannot be reconstructed safely.
