-- +goose Up
ALTER TABLE export_jobs DROP CONSTRAINT export_jobs_format_check;
ALTER TABLE export_jobs ADD CONSTRAINT export_jobs_format_check
    CHECK (format IN ('xlsx','csv','json','jsonl'));

-- +goose Down
-- Preserve existing dataset history while restoring the original format policy.
ALTER TABLE export_jobs DROP CONSTRAINT export_jobs_format_check;
ALTER TABLE export_jobs ADD CONSTRAINT export_jobs_format_check
    CHECK (format IN ('xlsx','csv','json')) NOT VALID;
