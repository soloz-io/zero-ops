import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { ory } from '@/lib/ory-client';

interface Session {
  id: string;
  active: boolean;
  identity: {
    id: string;
    traits: Record<string, unknown>;
  };
}

interface OryContextValue {
  session: Session | null;
  loading: boolean;
  refresh: () => Promise<void>;
  login: () => void;
  logout: () => void;
}

const OryContext = createContext<OryContextValue | null>(null);

function devAuthEnabled(): boolean {
  if (typeof window === 'undefined') return false;
  if (!import.meta.env.DEV) return false;
  if (import.meta.env.VITE_DEV_AUTH !== 'true') return false;
  return ['localhost', '127.0.0.1', '0.0.0.0'].includes(window.location.hostname);
}

function devSession(): Session | null {
  if (!devAuthEnabled()) return null;
  return {
    id: 'dev-session',
    active: true,
    identity: {
      id: 'dev-user',
      traits: {
        email: 'dev@local',
        role: 'platform_admin',
        tenant_id: 'local_tenant',
      },
    },
  };
}

export function OryProvider({ children }: { children: React.ReactNode }) {
  const [session, setSession] = useState<Session | null>(devSession);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    if (typeof window === 'undefined') return;
    if (devAuthEnabled()) {
      setSession(devSession());
      setLoading(false);
      return;
    }
    try {
      const res = await ory.toSession();
      const data = res as unknown as { id: string; active: boolean; identity: { id: string; traits: Record<string, unknown> } };
      setSession({
        id: data.id,
        active: data.active ?? true,
        identity: data.identity,
      });
    } catch {
      setSession(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const login = useCallback(() => {
    window.location.href = '/auth/login';
  }, []);

  const logout = useCallback(async () => {
    try {
      const res = await ory.createBrowserLogoutFlow();
      const data = res as unknown as { data: { logout_url: string } };
      window.location.href = data.data.logout_url;
    } catch {
      setSession(null);
      window.location.href = '/auth/login';
    }
  }, []);

  return (
    <OryContext.Provider value={{ session, loading, refresh, login, logout }}>
      {children}
    </OryContext.Provider>
  );
}

export function useOrySession(): OryContextValue {
  const ctx = useContext(OryContext);
  if (!ctx) {
    throw new Error('useOrySession must be used within an OryProvider');
  }
  return ctx;
}
