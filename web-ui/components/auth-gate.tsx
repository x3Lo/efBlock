"use client";

import { LayoutDashboard, ListFilter, LogOut, ScrollText, Settings, ShieldCheck, Users } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState } from "react";

import StatusIndicators from "@/components/status-indicators";
import ThemeToggle from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, UNAUTHORIZED_EVENT } from "@/lib/api";
import { cn } from "@/lib/utils";

type State = "loading" | "in" | "out";

// AuthGate : n'affiche l'application que si l'utilisateur est connecté ; sinon,
// la page de connexion. Les DONNÉES sont protégées par le serveur (l'API répond
// 401 sans session) : ce composant ne gère que l'affichage.
export default function AuthGate({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<State>("loading");

  useEffect(() => {
    api("/api/session")
      .then(() => setState("in"))
      .catch(() => setState("out"));
    const onUnauthorized = () => setState("out");
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, []);

  if (state === "loading") return <p className="p-8 text-muted-foreground">Chargement…</p>;
  if (state === "out") return <Login onSuccess={() => setState("in")} />;
  return <Shell onLogout={() => setState("out")}>{children}</Shell>;
}

const links = [
  { href: "/", label: "Tableau de bord", icon: LayoutDashboard },
  { href: "/queries/", label: "Journal", icon: ScrollText },
  { href: "/filters/", label: "Filtres", icon: ListFilter },
  { href: "/groups/", label: "Groupes", icon: Users },
  { href: "/settings/", label: "Réglages", icon: Settings },
];

function Brand() {
  return (
    <div className="flex items-center gap-2 px-2">
      <span className="flex size-7 items-center justify-center rounded-md bg-primary/15 text-primary">
        <ShieldCheck className="size-4" aria-hidden />
      </span>
      <span className="text-base font-semibold tracking-tight">efBlock</span>
    </div>
  );
}

function Shell({ children, onLogout }: { children: React.ReactNode; onLogout: () => void }) {
  const path = usePathname();
  const isCurrent = (href: string) => (href === "/" ? path === "/" : path.startsWith(href.replace(/\/$/, "")));

  const logout = useCallback(async () => {
    try {
      await api("/api/logout", { method: "POST" });
    } finally {
      onLogout();
    }
  }, [onLogout]);

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <aside className="sticky top-0 z-10 flex shrink-0 flex-col gap-3 border-b bg-card/95 px-3 py-3 backdrop-blur md:h-screen md:w-56 md:border-r md:border-b-0 md:bg-card md:py-5">
        <div className="flex items-center justify-between gap-3 md:block">
          <Brand />
          <div className="flex items-center gap-1 md:hidden">
            <ThemeToggle />
            <Button variant="ghost" size="sm" onClick={logout} className="text-muted-foreground" title="Se déconnecter">
              <LogOut className="size-4" aria-hidden />
              <span className="sr-only">Se déconnecter</span>
            </Button>
          </div>
        </div>
        <nav aria-label="Navigation principale" className="-mx-1 flex gap-1 overflow-x-auto px-1 md:mx-0 md:mt-3 md:flex-col md:overflow-visible md:px-0">
          {links.map((l) => {
            const Icon = l.icon;
            const current = isCurrent(l.href);
            return (
              <Link
                key={l.href}
                href={l.href}
                aria-current={current ? "page" : undefined}
                className={cn(
                  "flex shrink-0 items-center gap-2.5 rounded-md px-2.5 py-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                  current && "bg-primary/15 font-medium text-primary hover:bg-primary/20 hover:text-primary",
                )}
              >
                <Icon className="size-4 shrink-0" aria-hidden />
                {l.label}
              </Link>
            );
          })}
        </nav>
        <div className="md:mt-auto md:flex md:flex-col md:gap-2">
          <StatusIndicators />
          <div className="hidden flex-col md:flex">
            <ThemeToggle />
            <Button variant="ghost" size="sm" onClick={logout} className="justify-start gap-2 text-muted-foreground">
              <LogOut className="size-4" aria-hidden />
              Se déconnecter
            </Button>
          </div>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-4 py-5 md:px-8 md:py-7">
        <div className="mx-auto max-w-6xl">{children}</div>
      </main>
    </div>
  );
}

function Login({ onSuccess }: { onSuccess: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api("/api/login", { method: "POST", body: { password } });
      setPassword("");
      onSuccess();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Erreur de connexion");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto mt-[14vh] w-full max-w-sm px-4">
      <div className="rounded-xl border bg-card p-6 shadow-sm">
      <div className="mb-6 flex items-center gap-3">
        <span className="flex size-10 items-center justify-center rounded-lg bg-primary/15 text-primary">
          <ShieldCheck className="size-5" aria-hidden />
        </span>
        <div>
          <h1 className="text-lg font-semibold tracking-tight">efBlock</h1>
          <p className="text-xs text-muted-foreground">Bloqueur de publicités pour tout le réseau</p>
        </div>
      </div>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <label className="flex flex-col gap-1.5">
          <span className="text-xs text-muted-foreground">Mot de passe</span>
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            autoFocus
            required
          />
        </label>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button disabled={busy || !password}>{busy ? "Connexion…" : "Se connecter"}</Button>
      </form>
      </div>
    </div>
  );
}
