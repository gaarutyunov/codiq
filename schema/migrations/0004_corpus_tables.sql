-- +goose Up
ALTER TABLE file ADD COLUMN corpus text NOT NULL;

CREATE INDEX file_corpus_idx ON file USING btree (corpus);

-- +goose Down
DROP INDEX IF EXISTS file_corpus_idx;

ALTER TABLE file DROP COLUMN corpus;
