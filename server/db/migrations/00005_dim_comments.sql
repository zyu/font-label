-- +goose Up
-- 点评改为「按维度分别写」：五个维度各一条点评，每条至少 10 字（可配置）。
-- 旧的单条 comment 保留，作为五维点评的合并文本（导出/列表/历史沿用同名列）。
ALTER TABLE annotations ADD COLUMN dim_comments JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE rubrics ADD COLUMN dim_comment_min INT NOT NULL DEFAULT 10;
ALTER TABLE rubrics ADD COLUMN dim_comment_max INT NOT NULL DEFAULT 50;

-- +goose Down
ALTER TABLE rubrics DROP COLUMN dim_comment_max;
ALTER TABLE rubrics DROP COLUMN dim_comment_min;
ALTER TABLE annotations DROP COLUMN dim_comments;
