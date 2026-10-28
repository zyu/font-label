-- +goose Up
-- V5-N1 / V4-2：00008 只修正了当前方案，存量标注仍保留旧等级。
-- 按表5修正当前标注的派生等级，让列表、详情、审核和新导出读取同一值。
-- 直接使用保存的 total，不按现行权重重算；异常图和无总分记录不处理。
-- 原始评分快照、历史版本、分数、点评、提交时间和审核状态均保留。
-- 仅处理内置旧四档/现行表5档位；自定义档位不属于此次纠错范围。
-- 优先以提交快照判断，缺失快照时才回查原方案。双向包含忽略数组顺序。
WITH standard_bands(bands) AS (
    VALUES
        ('[{"label":"优秀","min":90},{"label":"良好","min":80},{"label":"中等","min":60},{"label":"较差","min":0}]'::jsonb),
        ('[{"label":"优秀","min":90},{"label":"良好","min":80},{"label":"中等","min":70},{"label":"及格","min":60},{"label":"需加油","min":0}]'::jsonb)
), stored AS (
    SELECT a.id, a.total,
           COALESCE(NULLIF(a.rubric_snapshot->'grade_bands', 'null'::jsonb), r.grade_bands) AS bands
    FROM annotations a
    LEFT JOIN rubrics r ON r.id = a.rubric_id
    WHERE a.kind = 'score' AND a.total BETWEEN 0 AND 100
), corrected AS (
    SELECT s.id, CASE
        WHEN s.total >= 90 THEN '优秀'
        WHEN s.total >= 80 THEN '良好'
        WHEN s.total >= 70 THEN '中等'
        WHEN s.total >= 60 THEN '及格'
        ELSE '需加油'
    END AS grade
    FROM stored s
    WHERE EXISTS (
        SELECT 1 FROM standard_bands b
        WHERE s.bands @> b.bands AND s.bands <@ b.bands
    )
)
UPDATE annotations a
SET grade = c.grade, updated_at = now()
FROM corrected c
WHERE a.id = c.id AND a.grade IS DISTINCT FROM c.grade;

-- +goose Down
-- 修正后的业务等级不回退；原始提交值仍可从 annotation_versions 追溯。
