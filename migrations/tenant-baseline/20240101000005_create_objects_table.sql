-- Migration: Create objects table with RLS
-- Description: Objects table for file metadata storage with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS public.objects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bucket_id UUID NOT NULL REFERENCES public.buckets(id) ON DELETE CASCADE,
  name VARCHAR(1024) NOT NULL,
  owner_id UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  size BIGINT NOT NULL,
  mime_type VARCHAR(255),
  storage_path TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb,
  UNIQUE(bucket_id, name)
);

-- Create index on bucket_id for bucket object queries
CREATE INDEX IF NOT EXISTS idx_objects_bucket_id ON public.objects(bucket_id);

-- Create index on owner_id for user object queries
CREATE INDEX IF NOT EXISTS idx_objects_owner_id ON public.objects(owner_id);

-- Create index on name for object name searches
CREATE INDEX IF NOT EXISTS idx_objects_name ON public.objects(name);

-- Create index on created_at for time-based queries
CREATE INDEX IF NOT EXISTS idx_objects_created_at ON public.objects(created_at);

-- Enable Row-Level Security
ALTER TABLE public.objects ENABLE ROW LEVEL SECURITY;

-- Drop existing policies if exist (for idempotency)
DROP POLICY IF EXISTS objects_owner_policy ON public.objects;
DROP POLICY IF EXISTS objects_public_bucket_read_policy ON public.objects;

-- Create RLS policy: owners can manage their objects
CREATE POLICY objects_owner_policy ON public.objects
  FOR ALL
  USING (
    owner_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    owner_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );

-- Create RLS policy: anyone can read objects in public buckets
CREATE POLICY objects_public_bucket_read_policy ON public.objects
  FOR SELECT
  USING (
    EXISTS (
      SELECT 1 FROM public.buckets
      WHERE buckets.id = objects.bucket_id AND buckets.public = TRUE
    )
  );

-- Drop existing trigger if exists (for idempotency)
DROP TRIGGER IF EXISTS update_objects_updated_at ON public.objects;

-- Create trigger to auto-update updated_at
CREATE TRIGGER update_objects_updated_at
  BEFORE UPDATE ON public.objects
  FOR EACH ROW
  EXECUTE FUNCTION public.update_updated_at_column();
