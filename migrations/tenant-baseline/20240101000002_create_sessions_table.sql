-- Migration: Create sessions table with RLS
-- Description: Sessions table for user authentication state with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS public.sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  token_hash VARCHAR(255) NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb
);

-- Create index on user_id for fast user session lookups
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON public.sessions(user_id);

-- Create index on token_hash for fast token validation
CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON public.sessions(token_hash);

-- Create index on expires_at for cleanup queries
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON public.sessions(expires_at);

-- Enable Row-Level Security
ALTER TABLE public.sessions ENABLE ROW LEVEL SECURITY;

-- Drop existing policy if exists (for idempotency)
DROP POLICY IF EXISTS sessions_isolation_policy ON public.sessions;

-- Create RLS policy: users can only access their own sessions
CREATE POLICY sessions_isolation_policy ON public.sessions
  FOR ALL
  USING (
    user_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    user_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );
