-- +goose Up
-- Old versions sampled rejections as well. Release only still-blocked tasks
-- whose current rejection was sampled. The historical decisions remain stored.
UPDATE images i SET quality_pending=false, updated_at=now()
FROM annotations a
WHERE a.image_id=i.id AND i.status='rejected' AND i.quality_pending
  AND EXISTS (
    SELECT 1 FROM quality_samples q JOIN reviews r ON r.id=q.review_id
    WHERE q.image_id=i.id AND r.annotation_version=a.version AND r.action='reject'
  );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assign_pending_quality_disputes() RETURNS void LANGUAGE plpgsql AS $$
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
        WHERE r.action='approve' AND d.arbiter_id IS NULL AND d.result IS NULL ORDER BY d.id
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

-- +goose Down
SELECT 1;
