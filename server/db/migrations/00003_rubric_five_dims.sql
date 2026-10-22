-- +goose Up
-- 打 分 口 径 升 级：四维（0~10 加权）→ 五维百分制（每维 20 分，直接相加）
-- 旧方案保留记录：历史标注的 rubric_snapshot 仍指向旧维度，导出/复核不受影响。
UPDATE rubrics SET is_active = false WHERE is_active = true;

INSERT INTO rubrics (name, dims, grade_bands, comment_min, comment_max, allow_total_override, is_active)
VALUES (
    '五维百分制（每维 20 分）',
    '[
      {"key":"white","label":"白平衡（匀）","weight":0.2,"max":20},
      {"key":"black","label":"黑平衡（稳）","weight":0.2,"max":20},
      {"key":"variation","label":"参差","weight":0.2,"max":20},
      {"key":"structure","label":"结构","weight":0.2,"max":20},
      {"key":"control","label":"控笔","weight":0.2,"max":20}
    ]'::jsonb,
    '[{"label":"优秀","min":90},{"label":"良好","min":80},
      {"label":"中等","min":60},{"label":"较差","min":0}]'::jsonb,
    20, 50, false, true
);

-- +goose Down
DELETE FROM rubrics WHERE name = '五维百分制（每维 20 分）';
UPDATE rubrics SET is_active = true WHERE name = '默认方案（等权）';
