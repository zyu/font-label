import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, getToken, setToken, setUnauthorizedHandler } from "./api/client";
import type { MeResponse, User } from "./api/types";

interface AuthState {
  user: User | null;
  me: MeResponse | null;
  ready: boolean;
  login: (username: string, password: string) => Promise<MeResponse>;
  logout: () => void;
  reload: () => Promise<void>;
}

const AuthContext = createContext<AuthState>(null as unknown as AuthState);

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [me, setMe] = useState<MeResponse | null>(null);
  const [ready, setReady] = useState(false);

  async function reload() {
    if (!getToken()) {
      setMe(null);
      setReady(true);
      return;
    }
    try {
      const data = await api.get<MeResponse>("/auth/me");
      setMe(data);
    } catch {
      setMe(null);
      setToken("");
    } finally {
      setReady(true);
    }
  }

  useEffect(() => {
    setUnauthorizedHandler(() => {
      queryClient.clear();
      setMe(null);
    });
    reload();
  }, [queryClient]);

  const value = useMemo<AuthState>(
    () => ({
      user: me?.user ?? null,
      me,
      ready,
      async login(username: string, password: string) {
        const res = await api.post<{ access_token: string; user: User }>("/auth/login", { username, password });
        queryClient.clear();
        setToken(res.access_token);
        const data = await api.get<MeResponse>("/auth/me");
        setMe(data);
        return data;
      },
      logout() {
        api.post("/auth/logout").catch(() => undefined);
        setToken("");
        queryClient.clear();
        setMe(null);
      },
      reload,
    }),
    [me, ready, queryClient],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  return useContext(AuthContext);
}
