-- +goose Up
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin','reviewer','annotator','quality'));

CREATE TABLE annotation_groups (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    archived BOOLEAN NOT NULL DEFAULT false,
    dispatch_count BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX uq_annotation_group_name ON annotation_groups (lower(name));

CREATE TABLE annotation_group_members (
    group_id BIGINT NOT NULL REFERENCES annotation_groups(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX idx_group_members_user ON annotation_group_members (user_id, group_id);

-- Source images keep their original IDs and files. Each group has a separate
-- workflow row so existing drafts, annotations, versions and reviews stay isolated.
ALTER TABLE images ADD COLUMN group_id BIGINT REFERENCES annotation_groups(id);
ALTER TABLE images ADD COLUMN source_image_id BIGINT REFERENCES images(id);
ALTER TABLE images ADD COLUMN quality_pending BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE images ADD COLUMN sample_no BIGINT;
ALTER TABLE images ADD CONSTRAINT image_group_source_pair CHECK (
    (group_id IS NULL) = (source_image_id IS NULL) AND (source_image_id IS NULL OR source_image_id <> id)
);
CREATE UNIQUE INDEX uq_images_group_source ON images (source_image_id, group_id);
CREATE INDEX idx_images_group_work ON images (group_id, status, annotator_id) WHERE group_id IS NOT NULL;

CREATE INDEX idx_group_task_navigation ON images (annotator_id,id)
    WHERE group_id IS NOT NULL AND status IN ('assigned','rejected') AND NOT quality_pending;

ALTER TABLE images DROP CONSTRAINT images_stored_name_key;
ALTER TABLE images DROP CONSTRAINT uq_images_day_seq;
DROP INDEX uq_images_folder_sha;
CREATE UNIQUE INDEX uq_images_source_name ON images (stored_name) WHERE group_id IS NULL;
CREATE UNIQUE INDEX uq_images_source_day_seq ON images (day, seq) WHERE group_id IS NULL;
CREATE UNIQUE INDEX uq_images_folder_sha ON images (folder_id, sha256) WHERE group_id IS NULL;
CREATE UNIQUE INDEX uq_images_sample_no ON images (sample_no) WHERE group_id IS NULL;

CREATE TABLE image_sample_counter (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    last_no BIGINT NOT NULL
);
WITH numbered AS (
    SELECT id, row_number() OVER (ORDER BY id) AS sample_no FROM images
), backfilled AS (
    UPDATE images i SET sample_no=n.sample_no FROM numbered n WHERE n.id=i.id
    RETURNING i.sample_no
)
INSERT INTO image_sample_counter VALUES (true, (SELECT COALESCE(max(sample_no),0) FROM backfilled));

ALTER TABLE images ALTER COLUMN sample_no SET NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION assign_source_sample_no() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.group_id IS NULL THEN
        UPDATE image_sample_counter SET last_no=last_no+1
        WHERE singleton RETURNING last_no INTO NEW.sample_no;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER assign_source_sample_no BEFORE INSERT ON images
    FOR EACH ROW EXECUTE FUNCTION assign_source_sample_no();

CREATE TABLE quality_policy (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    sample_rate_bps INT NOT NULL CHECK (sample_rate_bps BETWEEN 0 AND 10000),
    strata TEXT[] NOT NULL DEFAULT '{}',
    revision BIGINT NOT NULL DEFAULT 1,
    last_arbiter_id BIGINT REFERENCES users(id)
);
INSERT INTO quality_policy (singleton,sample_rate_bps) VALUES (true,500);

CREATE TABLE quality_strata (
    revision BIGINT NOT NULL,
    group_key BIGINT NOT NULL,
    folder_key BIGINT NOT NULL,
    review_key TEXT NOT NULL,
    last_no BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (revision,group_key,folder_key,review_key)
);

CREATE TABLE quality_samples (
    id BIGSERIAL PRIMARY KEY,
    review_id BIGINT NOT NULL UNIQUE REFERENCES reviews(id),
    image_id BIGINT NOT NULL REFERENCES images(id),
    sample_order BIGINT NOT NULL CHECK (sample_order > 0),
    inspector_id BIGINT REFERENCES users(id),
    result TEXT CHECK (result IN ('pass','fail')),
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_quality_samples_inspector ON quality_samples (inspector_id, id) WHERE result IS NULL;
CREATE INDEX idx_quality_samples_image ON quality_samples (image_id);

CREATE TABLE quality_disputes (
    id BIGSERIAL PRIMARY KEY,
    sample_id BIGINT NOT NULL UNIQUE REFERENCES quality_samples(id),
    arbiter_id BIGINT NOT NULL REFERENCES users(id),
    result TEXT CHECK (result IN ('return','approve')),
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_quality_disputes_arbiter ON quality_disputes (arbiter_id, id) WHERE result IS NULL;

-- +goose Down
-- Group tasks and quality decisions are production data. Reverse through a
-- forward migration that explicitly preserves their history.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'group workflow migration is irreversible';
END $$;
-- +goose StatementEnd
