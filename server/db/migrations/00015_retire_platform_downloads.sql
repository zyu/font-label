-- +goose Up
UPDATE export_jobs SET status='failed', error='平台下载已停用，请通过 OSS 上传取数', finished_at=now()
WHERE destination='download' AND status IN ('pending','running');

-- +goose Down
SELECT 1;
