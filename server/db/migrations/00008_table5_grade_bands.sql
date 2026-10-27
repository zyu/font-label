-- +goose Up
-- 评分规则 4.2 表5：保留维度、权重与点评限制，仅修正当前方案的总分档位。
-- 历史标注的 grade 与 rubric_snapshot 保持原样。
UPDATE rubrics
SET grade_bands = '[{"label":"优秀","min":90},{"label":"良好","min":80},{"label":"中等","min":70},{"label":"及格","min":60},{"label":"需加油","min":0}]'::jsonb
WHERE is_active = true;

-- +goose Down
-- Data correction is intentionally retained; customized former bands cannot be
-- reconstructed safely. Use a new forward migration to change the grading rule.
