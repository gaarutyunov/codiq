-- +goose Up
CREATE TABLE file (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    path text NOT NULL,
    lang text NOT NULL,
    pkg_scheme text NOT NULL,
    pkg_manager text NOT NULL,
    pkg_name text NOT NULL,
    pkg_version text NOT NULL
);

CREATE INDEX file_path_idx ON file USING btree (path);

CREATE INDEX file_lang_idx ON file USING btree (lang);

CREATE TABLE occurrence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id uuid NOT NULL,
    descriptor text NOT NULL,
    role text NOT NULL,
    symbol_kind text NOT NULL,
    name text NOT NULL,
    range_start integer NOT NULL,
    range_end integer NOT NULL,
    scope_id uuid,
    CONSTRAINT occurrence_role_check CHECK (role IN ('definition', 'reference'))
);

CREATE INDEX occurrence_file_id_idx ON occurrence USING btree (file_id);

CREATE INDEX occurrence_descriptor_idx ON occurrence USING btree (descriptor);

CREATE INDEX occurrence_name_idx ON occurrence USING btree (name);

CREATE TABLE scope (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id uuid NOT NULL,
    kind text NOT NULL,
    range_start integer NOT NULL,
    range_end integer NOT NULL,
    parent_scope_id uuid
);

CREATE INDEX scope_file_id_idx ON scope USING btree (file_id);

CREATE TABLE calls (
    source_id uuid NOT NULL REFERENCES occurrence (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX calls_target_idx ON calls (target_id);

CREATE TABLE contains_occurrence (
    source_id uuid NOT NULL REFERENCES scope (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX contains_occurrence_target_idx ON contains_occurrence (target_id);

CREATE TABLE contains_scope (
    source_id uuid NOT NULL REFERENCES scope (id),
    target_id uuid NOT NULL REFERENCES scope (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX contains_scope_target_idx ON contains_scope (target_id);

CREATE TABLE defines (
    source_id uuid NOT NULL REFERENCES file (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX defines_target_idx ON defines (target_id);

CREATE TABLE implements (
    source_id uuid NOT NULL REFERENCES occurrence (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX implements_target_idx ON implements (target_id);

CREATE TABLE imports (
    source_id uuid NOT NULL REFERENCES file (id),
    target_id uuid NOT NULL REFERENCES file (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX imports_target_idx ON imports (target_id);

CREATE TABLE references_local (
    source_id uuid NOT NULL REFERENCES occurrence (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX references_local_target_idx ON references_local (target_id);

CREATE TABLE resolves_to (
    source_id uuid NOT NULL REFERENCES occurrence (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX resolves_to_target_idx ON resolves_to (target_id);

CREATE TABLE type_defines (
    source_id uuid NOT NULL REFERENCES occurrence (id),
    target_id uuid NOT NULL REFERENCES occurrence (id),
    PRIMARY KEY (source_id, target_id)
);

CREATE INDEX type_defines_target_idx ON type_defines (target_id);

-- +goose Down
DROP TABLE IF EXISTS calls;

DROP TABLE IF EXISTS contains_occurrence;

DROP TABLE IF EXISTS contains_scope;

DROP TABLE IF EXISTS defines;

DROP TABLE IF EXISTS implements;

DROP TABLE IF EXISTS imports;

DROP TABLE IF EXISTS references_local;

DROP TABLE IF EXISTS resolves_to;

DROP TABLE IF EXISTS type_defines;

DROP TABLE IF EXISTS file;

DROP TABLE IF EXISTS occurrence;

DROP TABLE IF EXISTS scope;
