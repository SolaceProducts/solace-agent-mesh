
-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE c text;
BEGIN
    FOR c IN SELECT conname FROM pg_constraint
        WHERE conrelid = 'finding_eval_sets'::regclass AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%refused%'
          AND (pg_get_constraintdef(oid) LIKE '%drafting%' OR pg_get_constraintdef(oid) LIKE '%reason_code IS NOT NULL%')
    LOOP
        EXECUTE 'ALTER TABLE finding_eval_sets DROP CONSTRAINT ' || quote_ident(c);
    END LOOP;
END $$;
-- +goose StatementEnd
ALTER TABLE finding_eval_sets ADD CONSTRAINT finding_eval_sets_status_check
    CHECK (status IN ('drafting', 'drafted', 'declined', 'refused', 'failed'));
ALTER TABLE finding_eval_sets ADD CONSTRAINT finding_eval_sets_reason_check
    CHECK ((status IN ('declined', 'refused', 'failed')) = (reason_code IS NOT NULL));

-- +goose Down
DELETE FROM finding_eval_sets WHERE status = 'declined';
ALTER TABLE finding_eval_sets DROP CONSTRAINT IF EXISTS finding_eval_sets_reason_check;
ALTER TABLE finding_eval_sets DROP CONSTRAINT IF EXISTS finding_eval_sets_status_check;
ALTER TABLE finding_eval_sets ADD CONSTRAINT finding_eval_sets_status_check
    CHECK (status IN ('drafting', 'drafted', 'refused', 'failed'));
ALTER TABLE finding_eval_sets ADD CONSTRAINT finding_eval_sets_reason_check
    CHECK ((status IN ('refused', 'failed')) = (reason_code IS NOT NULL));
