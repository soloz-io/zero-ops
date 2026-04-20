-- Migration: Create buckets table with RLS
-- Description: Buckets table for object storage organization with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS public.buckets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) NOT NULL UNIQUE,
  owner_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  public BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb
);

-- Create index on name for fast bucket lookups
CREATE INDEX IF NOT EXISTS idx_buckets_name ON public.buckets(name);

-- Create index on owner_id for user bucket queries
CREATE INDEX IF NOT EXISTS idx_buckets_owner_id ON public.buckets(owner_id);

-- Create index on public for public bucket queries
CREATE INDEX IF NOT EXISTS idx_buckets_public ON public.buckets(public);

-- Enable Row-Level Security
ALTER TABLE public.buckets ENABLE ROW LEVEL SECURITY;

-- Drop existing policies if exist (for idempotency)
DROP POLICY IF EXISTS buckets_owner_policy ON public.buckets;
DROP POLICY IF EXISTS buckets_public_read_policy ON public.buckets;

-- Create RLS policy: owners can manage their buckets
CREATE POLICY buckets_owner_policy ON public.buckets
  FOR ALL
  USING (
    owner_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    owner_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );

-- Create RLS policy: anyone can read public buckets
CREATE POLICY buckets_public_read_policy ON public.buckets
  FOR SELECT
  USING (public = TRUE);

-- Drop existing trigger if exists (for idempotency)
DROP TRIGGER IF EXISTS update_buckets_updated_at ON public.buckets;

-- Create trigger to auto-update updated_at
CREATE TRIGGER update_buckets_updated_at
  BEFORE UPDATE ON public.buckets
  FOR EACH ROW
  EXECUTE FUNCTION public.update_updated_at_column();
