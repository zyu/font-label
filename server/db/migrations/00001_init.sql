-- +goose Up
-- +goose StatementBegin
CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    username        TEXT        NOT NULL UNIQUE,
    password_hash   TEXT        NOT NULL,
    display_name    TEXT        NOT NULL,
    role            TEXT        NOT NULL CHECK (role IN ('admin','reviewer','annotator')),
    status          TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    token_version   INT         NOT NULL DEFAULT 0,
    must_change_pwd BOOLEAN     NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ,
    created_by      BIGINT      REFERENCES users(id)
);
CREATE INDEX idx_users_role ON users(role) WHERE status = 'active';

CREATE TABLE folders (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL,
    note        TEXT        NOT NULL DEFAULT '',
    created_by  BIGINT      REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_folders_name ON folders (lower(name));

CREATE TABLE daily_counters (
    day      DATE PRIMARY KEY,
    last_seq INT  NOT NULL DEFAULT 0
);

CREATE TABLE images (
    id            BIGSERIAL PRIMARY KEY,
    folder_id     BIGINT      NOT NULL REFERENCES folders(id),
    stored_name   TEXT        NOT NULL UNIQUE,
    day           DATE        NOT NULL,
    seq           INT         NOT NULL,
    orig_name     TEXT        NOT NULL,
    ext           TEXT        NOT NULL,
    mime          TEXT        NOT NULL,
    bytes         BIGINT      NOT NULL,
    width         INT,
    height        INT,
    sha256        CHAR(64)    NOT NULL,
    rel_path      TEXT        NOT NULL,
    thumb_path    TEXT,
    preview_path  TEXT,
    status        TEXT        NOT NULL DEFAULT 'unassigned'
                  CHECK (status IN ('unassigned','assigned','submitted','approved','rejected','excluded')),
    annotator_id  BIGINT      REFERENCES users(id),
    claimed_at    TIMESTAMPTZ,
    lock_expires_at TIMESTAMPTZ,
    submitted_at  TIMESTAMPTZ,
    reviewed_at   TIMESTAMPTZ,
    review_count  INT         NOT NULL DEFAULT 0,
    reject_count  INT         NOT NULL DEFAULT 0,
    uploaded_by   BIGINT      REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_images_day_seq UNIQUE (day, seq)
);
CREATE INDEX idx_images_pool ON images (folder_id, seq) WHERE status = 'unassigned';
CREATE INDEX idx_images_owner ON images (annotator_id, status, claimed_at DESC);
CREATE INDEX idx_images_review_queue ON images (submitted_at) WHERE status = 'submitted';
CREATE INDEX idx_images_created ON images (created_at);
CREATE INDEX idx_images_status ON images (status);
CREATE UNIQUE INDEX uq_images_folder_sha ON images (folder_id, sha256);

CREATE TABLE annotations (
    id                BIGSERIAL PRIMARY KEY,
    image_id          BIGINT      NOT NULL UNIQUE REFERENCES images(id) ON DELETE CASCADE,
    annotator_id      BIGINT      NOT NULL REFERENCES users(id),
    version           INT         NOT NULL DEFAULT 1,
    kind              TEXT        NOT NULL CHECK (kind IN ('score','exception')),
    scores            JSONB,
    total             NUMERIC(5,1),
    grade             TEXT,
    comment           TEXT        NOT NULL DEFAULT '',
    exception_reasons TEXT[]      NOT NULL DEFAULT '{}',
    exception_note    TEXT        NOT NULL DEFAULT '',
    rubric_id         BIGINT,
    rubric_snapshot   JSONB,
    duration_ms       INT,
    client_submit_id  UUID,
    submitted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ann_annotator ON annotations (annotator_id, submitted_at DESC);
CREATE INDEX idx_ann_kind ON annotations (kind);
CREATE UNIQUE INDEX uq_ann_submit ON annotations (client_submit_id) WHERE client_submit_id IS NOT NULL;

CREATE TABLE annotation_versions (
    id           BIGSERIAL PRIMARY KEY,
    image_id     BIGINT      NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    version      INT         NOT NULL,
    annotator_id BIGINT      NOT NULL,
    payload      JSONB       NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (image_id, version)
);
CREATE INDEX idx_annver_image ON annotation_versions (image_id, version DESC);

CREATE TABLE reviews (
    id            BIGSERIAL PRIMARY KEY,
    image_id      BIGINT      NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    annotation_id BIGINT      NOT NULL REFERENCES annotations(id),
    annotation_version INT    NOT NULL,
    reviewer_id   BIGINT      NOT NULL REFERENCES users(id),
    action        TEXT        NOT NULL CHECK (action IN ('approve','reject')),
    comment       TEXT        NOT NULL DEFAULT '',
    reason_tags   TEXT[]      NOT NULL DEFAULT '{}',
    score_patch   JSONB,
    drawings      JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_reviews_image ON reviews (image_id, created_at DESC);
CREATE INDEX idx_reviews_reviewer ON reviews (reviewer_id, created_at DESC);
CREATE INDEX idx_reviews_reject ON reviews (created_at) WHERE action = 'reject';

CREATE TABLE drafts (
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    image_id   BIGINT      NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    payload    JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, image_id)
);

CREATE TABLE claims (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id),
    mode          TEXT        NOT NULL CHECK (mode IN ('count','folder')),
    requested     INT         NOT NULL DEFAULT 0,
    granted       INT         NOT NULL DEFAULT 0,
    folder_id     BIGINT      REFERENCES folders(id),
    image_ids     BIGINT[]    NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at   TIMESTAMPTZ,
    release_reason TEXT
);
CREATE INDEX idx_claims_user ON claims (user_id, created_at DESC);

CREATE TABLE rubrics (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT        NOT NULL,
    dims          JSONB       NOT NULL,
    grade_bands   JSONB       NOT NULL,
    comment_min   INT         NOT NULL DEFAULT 20,
    comment_max   INT         NOT NULL DEFAULT 50,
    allow_total_override BOOLEAN NOT NULL DEFAULT false,
    is_active     BOOLEAN     NOT NULL DEFAULT false,
    created_by    BIGINT      REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_rubric_active ON rubrics (is_active) WHERE is_active;

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_by BIGINT      REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE export_jobs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT      NOT NULL REFERENCES users(id),
    format      TEXT        NOT NULL CHECK (format IN ('xlsx','csv','json')),
    filters     JSONB       NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','running','done','failed')),
    file_path   TEXT,
    row_count   INT,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ
);

CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    BIGINT      REFERENCES users(id),
    action      TEXT        NOT NULL,
    target_type TEXT        NOT NULL,
    target_id   BIGINT,
    detail      JSONB       NOT NULL DEFAULT '{}',
    ip          INET,
    ua          TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_actor ON audit_logs (actor_id, created_at DESC);
CREATE INDEX idx_audit_target ON audit_logs (target_type, target_id);
CREATE INDEX idx_audit_action ON audit_logs (action, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit_logs, export_jobs, settings, rubrics, claims, drafts,
    reviews, annotation_versions, annotations, images, daily_counters, folders, users CASCADE;
-- +goose StatementEnd
