-- Problem-set item counts are entered as one exact number by the current UI.
-- Keep the old range columns for wire/storage compatibility, but widen their
-- domain so a direct count is not artificially capped at 20.
DO $$
DECLARE
    constraint_row RECORD;
BEGIN
    FOR constraint_row IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'problem_sets'::regclass
          AND contype = 'c'
          AND (
              pg_get_constraintdef(oid) LIKE '%desired_item_count%'
              OR pg_get_constraintdef(oid) LIKE '%min_item_count%'
              OR pg_get_constraintdef(oid) LIKE '%max_item_count%'
          )
    LOOP
        EXECUTE format('ALTER TABLE problem_sets DROP CONSTRAINT %I', constraint_row.conname);
    END LOOP;
END
$$;

ALTER TABLE problem_sets
    ADD CONSTRAINT problem_sets_desired_item_count_v2_check
        CHECK (desired_item_count >= 0 AND desired_item_count <= 1000),
    ADD CONSTRAINT problem_sets_min_item_count_v2_check
        CHECK (min_item_count >= 1 AND min_item_count <= 1000),
    ADD CONSTRAINT problem_sets_max_item_count_v2_check
        CHECK (max_item_count >= 1 AND max_item_count <= 1000),
    ADD CONSTRAINT problem_sets_item_count_order_v2_check
        CHECK (min_item_count <= max_item_count),
    ADD CONSTRAINT problem_sets_desired_item_count_range_v2_check
        CHECK (desired_item_count = 0 OR
               (desired_item_count >= min_item_count AND desired_item_count <= max_item_count));
