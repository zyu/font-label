-- +goose Up
ALTER TABLE annotations ADD COLUMN grades JSONB;
ALTER TABLE reviews ADD COLUMN grade_patch JSONB;

-- Convert historical numeric values using the scale shown by their editor.
-- +goose StatementBegin
CREATE FUNCTION migrate_dimension_grades(values_json JSONB, dimensions JSONB)
RETURNS JSONB LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_object_agg(entry.key, picked.grade), '{}'::jsonb)
    FROM jsonb_each(CASE WHEN jsonb_typeof(values_json)='object' THEN values_json ELSE '{}' END) entry
    LEFT JOIN LATERAL (
        SELECT CASE WHEN (d->>'max')::int>0 THEN (d->>'max')::int ELSE 10 END AS max_score,
               greatest(COALESCE((d->>'step')::int,1),1) AS step
        FROM jsonb_array_elements(CASE WHEN jsonb_typeof(dimensions)='array' THEN dimensions ELSE '[]' END) d WHERE d->>'key'=entry.key
        LIMIT 1
    ) dim ON true
    CROSS JOIN LATERAL (
        SELECT grade FROM (VALUES ('S',4),('A',3),('B',2),('C',1),('D',0)) levels(grade, rank)
        ORDER BY CASE WHEN entry.value='0'::jsonb AND rank=0 THEN 0 ELSE 1 END,
                 abs(CASE WHEN jsonb_typeof(entry.value)='number' THEN (entry.value::text)::numeric END
                     - round(rank * (COALESCE(dim.max_score,20)/COALESCE(dim.step,5)) / 4.0) * COALESCE(dim.step,5)),
                 rank DESC
        LIMIT 1
    ) picked
    WHERE jsonb_typeof(entry.value)='number'
$$;
-- +goose StatementEnd

UPDATE annotations
SET grades=migrate_dimension_grades(scores, COALESCE(rubric_snapshot->'dims',(SELECT dims FROM rubrics WHERE id=annotations.rubric_id))), comment=''
WHERE kind='score';

UPDATE reviews r SET grade_patch=migrate_dimension_grades(r.score_patch, COALESCE(a.rubric_snapshot->'dims',(SELECT dims FROM rubrics WHERE id=a.rubric_id)))
FROM annotations a WHERE a.id=r.annotation_id AND r.score_patch IS NOT NULL;

UPDATE annotation_versions v SET payload=
    (v.payload - ARRAY['scores','total','grade','comment']) ||
    CASE WHEN v.payload->>'kind'='score' OR jsonb_typeof(v.payload->'scores')='object' THEN
      jsonb_build_object('grades',migrate_dimension_grades(v.payload->'scores', COALESCE(v.payload->'rubric_snapshot'->'dims',COALESCE(a.rubric_snapshot->'dims',(SELECT dims FROM rubrics WHERE id=a.rubric_id)))))
    ELSE jsonb_build_object('exception_note',COALESCE(v.payload->>'exception_note', v.payload->>'comment','')) END
FROM annotations a WHERE a.image_id=v.image_id;

UPDATE drafts SET payload=(payload - ARRAY['scores','total','grade']) ||
    jsonb_build_object('grades',migrate_dimension_grades(payload->'scores',
        (SELECT dims FROM rubrics WHERE is_active LIMIT 1)))
WHERE jsonb_typeof(payload)='object' AND payload ? 'scores';

DROP FUNCTION migrate_dimension_grades(JSONB,JSONB);
ALTER TABLE annotations ADD CONSTRAINT annotations_grades_check CHECK (
    grades IS NULL OR (jsonb_typeof(grades)='object' AND NOT jsonb_path_exists(grades,
        '$.* ? (@.type() != "string" || @ != "S" && @ != "A" && @ != "B" && @ != "C" && @ != "D")'))
);
ALTER TABLE annotations DROP COLUMN scores, DROP COLUMN total, DROP COLUMN grade;
ALTER TABLE reviews DROP COLUMN score_patch;

UPDATE rubrics r SET dims=(SELECT jsonb_agg(jsonb_build_object('key',d->'key','label',d->'label') ORDER BY n)
    FROM jsonb_array_elements(r.dims) WITH ORDINALITY AS dimensions(d,n)),
    name=CASE WHEN r.name LIKE '五维百分制%' THEN '五维等级制' ELSE r.name END;
UPDATE rubrics SET is_active=false WHERE is_active AND NOT (
   jsonb_array_length(dims)=5 AND dims @> '[{"key":"white"},{"key":"black"},{"key":"variation"},{"key":"structure"},{"key":"control"}]'
);
INSERT INTO rubrics (name,dims,grade_bands,comment_min,comment_max,allow_total_override,dim_comment_min,dim_comment_max,is_active)
SELECT '五维等级制','[{"key":"white","label":"白平衡（匀）"},{"key":"black","label":"黑平衡（稳）"},{"key":"variation","label":"参差"},{"key":"structure","label":"结构"},{"key":"control","label":"控笔"}]',
       '[]',20,50,false,10,50,true
WHERE NOT EXISTS (SELECT 1 FROM rubrics WHERE is_active);
ALTER TABLE rubrics DROP COLUMN grade_bands, DROP COLUMN comment_min, DROP COLUMN comment_max, DROP COLUMN allow_total_override;

INSERT INTO settings (key,value) VALUES ('exception_reasons','[
    {"key":"wrong_char","label":"错字（笔画写错、多笔少笔）"},
    {"key":"illegible","label":"无法辨认"},
    {"key":"blank","label":"空白（无书写内容）"},
    {"key":"multi_char","label":"多字（一图多字）"},
    {"key":"non_writing","label":"非书写内容（图画、印刷体等）"},
    {"key":"blurry","label":"图片模糊（看不清笔画）"}
]') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=now();
UPDATE settings SET value=replace(replace(value::text,'分数与点评不符','等级与点评不符'),'某维度分数明显偏高/偏低','某维度等级明显偏高/偏低')::jsonb
WHERE key='reject_reason_tags';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Numeric ratings cannot be reconstructed from letter ratings'; END $$;
-- +goose StatementEnd
