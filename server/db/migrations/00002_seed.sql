-- +goose Up
INSERT INTO rubrics (name, dims, grade_bands, comment_min, comment_max, allow_total_override, is_active)
VALUES (
    '默认方案（等权）',
    '[{"key":"fill","label":"撑满","weight":0.25,"max":10},
      {"key":"space","label":"布匀","weight":0.25,"max":10},
      {"key":"balance","label":"挂稳","weight":0.25,"max":10},
      {"key":"variation","label":"参差","weight":0.25,"max":10}]'::jsonb,
    '[{"label":"优秀","min":90},{"label":"良好","min":80},
      {"label":"中等","min":60},{"label":"较差","min":0}]'::jsonb,
    20, 50, false, true
);

INSERT INTO settings (key, value) VALUES
 ('lock_ttl_hours', '24'::jsonb),
 ('max_hold_per_user', '100'::jsonb),
 ('claim_default_count', '10'::jsonb),
 ('max_claim_count', '100'::jsonb),
 ('max_upload_files', '200'::jsonb),
 ('max_file_mb', '20'::jsonb),
 ('auto_next_after_submit', 'true'::jsonb),
 ('exception_reasons', '[
   {"key":"tilt","label":"图片倾斜超过 15°"},
   {"key":"blur","label":"严重失焦，笔画糊成一团"},
   {"key":"exposure","label":"过曝 / 过暗，看不清笔画"},
   {"key":"privacy","label":"含学生姓名、班级等未打码信息"},
   {"key":"occluded","label":"手指 / 阴影 / 杂物遮挡文字"},
   {"key":"empty","label":"无有效书写内容 / 重复图片"}]'::jsonb),
 ('reject_reason_tags', '[
   "分数与点评不符",
   "某维度分数明显偏高/偏低",
   "点评太笼统，未指出具体问题",
   "未覆盖示例要求的维度",
   "异常判定不成立",
   "图片与点评内容不对应"]'::jsonb),
 ('comment_templates', '[
   "撑满到位，四周留白均匀。",
   "布匀略欠，横向笔画间距不均。",
   "重心偏左，注意中轴对齐。",
   "撇捺出锋干净，姿态舒展。",
   "主笔不够放开，注意长短变化。",
   "整体稳，留意右侧收笔不要出格。"]'::jsonb)
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM settings WHERE key IN ('lock_ttl_hours','max_hold_per_user','claim_default_count',
  'max_claim_count','max_upload_files','max_file_mb','auto_next_after_submit',
  'exception_reasons','reject_reason_tags','comment_templates');
DELETE FROM rubrics WHERE name = '默认方案（等权）';
