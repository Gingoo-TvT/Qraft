-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;
-- Enable uuid-ossp for UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
-- Enable pg_trgm for text search
CREATE EXTENSION IF NOT EXISTS pg_trgm;
