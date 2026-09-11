-- Extend problem_level enum with GPLT (团体程序设计天梯赛) tiers.
-- PostgreSQL requires ADD VALUE statements to run outside a transaction block
-- (some migration tools handle this automatically). These values cannot be
-- removed once added, but the change is purely additive and non-breaking.

ALTER TYPE problem_level ADD VALUE IF NOT EXISTS 'gplt_l1';
ALTER TYPE problem_level ADD VALUE IF NOT EXISTS 'gplt_l2';
ALTER TYPE problem_level ADD VALUE IF NOT EXISTS 'gplt_l3';
