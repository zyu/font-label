-- +goose Up
-- 打分粒度改为固定档位：每维只能取 0/5/10/15/20（step=5）
UPDATE rubrics
SET dims = (
    SELECT jsonb_agg(d || '{"step":5}'::jsonb ORDER BY ord)
    FROM jsonb_array_elements(dims) WITH ORDINALITY AS t(d, ord)
)
WHERE is_active = true;

-- +goose Down
UPDATE rubrics
SET dims = (
    SELECT jsonb_agg(d - 'step' ORDER BY ord)
    FROM jsonb_array_elements(dims) WITH ORDINALITY AS t(d, ord)
)
WHERE is_active = true;
