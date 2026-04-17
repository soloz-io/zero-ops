-- Migration: Create identities table with RLS
-- Description: Identities table for OAuth/OIDC provider linkage with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS public.identities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  provider VARCHAR(50) NOT NULL,
  provider_user_id VARCHAR(255) NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb,
  UNIQUE(provider, provider_user_id)
);

-- Create index on user_id for fast user identity lookups
CREATE INDEX IF NOT EXISTS idx_identities_user_id ON public.identities(user_id);

-- Create index on provider and provider_user_id for OAuth lookups
CREATE INDEX IF NOT EXISTS idx_identities_provider ON public.identities(provider, provider_user_id);

-- Enable Row-Level Security
ALTER TABLE public.identities ENABLE ROW LEVEL SECURITY;

-- Drop existing policy if exists (for idempotency)
DROP POLICY IF EXISTS identities_isolation_policy ON public.identities;

-- Create RLS policy: users can only access their own identities
CREATE POLICY identities_isolation_policy ON public.identities
  FOR ALL
  USING (
    user_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    user_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );

-- Drop existing trigger if exists (for idempotency)
DROP TRIGGER IF EXISTS update_identities_updated_at ON public.identities;

-- Create trigger to auto-update updated_at
CREATE TRIGGER update_identities_updated_at
  BEFORE UPDATE ON public.identities
  FOR EACH ROW
  EXECUTE FUNCTION public.update_updated_at_column();
