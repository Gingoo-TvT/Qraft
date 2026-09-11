-- PostgreSQL can move a binary-compatible VARCHAR[] -> TEXT[] cast from the
-- array to each element when pg_dump output is parsed during restore. Rebuild
-- only the affected expressions with explicit TEXT literals so their catalog
-- definitions are stable across the first and subsequent restore cycles.

ALTER TABLE public.workflow_operations
    DROP CONSTRAINT workflow_operations_status_check,
    ADD CONSTRAINT workflow_operations_status_check
        CHECK (status::text = ANY (ARRAY['in_progress'::text, 'failed'::text, 'completed'::text]));

ALTER TABLE public.workflow_outbox
    DROP CONSTRAINT workflow_outbox_status_check,
    ADD CONSTRAINT workflow_outbox_status_check
        CHECK (status::text = ANY (ARRAY['pending'::text, 'processing'::text, 'retry'::text, 'delivered'::text, 'dead'::text]));

ALTER TABLE public.provider_effects
    DROP CONSTRAINT provider_effects_status_check,
    ADD CONSTRAINT provider_effects_status_check
        CHECK (status::text = ANY (ARRAY['in_progress'::text, 'completed'::text]));

DROP INDEX public.idx_workflow_outbox_ready;

CREATE INDEX idx_workflow_outbox_ready
    ON public.workflow_outbox (available_at, created_at)
    WHERE status::text = ANY (ARRAY['pending'::text, 'retry'::text]);
