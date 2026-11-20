-- +goose Up
ALTER TABLE quality_disputes ALTER COLUMN arbiter_id DROP NOT NULL;

UPDATE quality_disputes d SET arbiter_id=NULL
FROM quality_samples q WHERE q.id=d.sample_id AND d.result IS NULL AND d.arbiter_id=q.inspector_id;

-- +goose StatementBegin
CREATE FUNCTION assign_pending_quality_disputes() RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    pending record;
    previous_id bigint;
    next_id bigint;
BEGIN
    SELECT COALESCE(last_arbiter_id,0) INTO previous_id FROM quality_policy WHERE singleton FOR UPDATE;
    FOR pending IN
        SELECT d.id,q.inspector_id
        FROM quality_disputes d JOIN quality_samples q ON q.id=d.sample_id
        JOIN reviews r ON r.id=q.review_id
        JOIN images i ON i.id=q.image_id AND r.annotation_version>i.voided_through_version
        WHERE d.arbiter_id IS NULL AND d.result IS NULL ORDER BY d.id
    LOOP
        SELECT id INTO next_id FROM users WHERE role='quality' AND status='active'
          AND id IS DISTINCT FROM pending.inspector_id ORDER BY (id<=previous_id),id LIMIT 1;
        IF next_id IS NOT NULL THEN
            UPDATE quality_disputes SET arbiter_id=next_id WHERE id=pending.id;
            UPDATE quality_policy SET last_arbiter_id=next_id WHERE singleton;
            previous_id := next_id;
        END IF;
    END LOOP;
END $$;
-- +goose StatementEnd

SELECT assign_pending_quality_disputes();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Pending disputes may have no arbiter'; END $$;
-- +goose StatementEnd
