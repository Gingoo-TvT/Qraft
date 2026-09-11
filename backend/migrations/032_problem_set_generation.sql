-- Additive configuration and durable progress for automated mixed problem sets.
ALTER TABLE problem_sets
    ADD COLUMN IF NOT EXISTS generation_config JSONB,
    ADD COLUMN IF NOT EXISTS generation_state JSONB;
