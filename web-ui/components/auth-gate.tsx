"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState } from "react";

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

const links: { href: string; label: string; external?: boolean }[] = [
  { href: "/", label: "Tableau de bord" },
  { href: "/queries/", label: "Journal" },
  { href: "/filters/", label: "Filtres" },
  { href: "/groups/", label: "Groupes" },
  { href: "/settings/", label: "Réglages" },
  { href: "/test.html", label: "Test du blocage", external: true },
];

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
      <aside className="flex shrink-0 flex-col gap-3 border-b bg-card px-3 py-3 md:min-h-screen md:w-52 md:border-r md:border-b-0 md:py-4">
        <div className="px-2 text-base font-semibold tracking-tight">efBlock</div>
        <nav aria-label="Navigation principale" className="flex gap-0.5 md:flex-col">
          {links.map((l) =>
            l.external ? (
              // Page de test autonome (fichier statique) : elle s'ouvre dans un nouvel onglet,
              // et la même adresse peut être ouverte sur d'autres appareils pour comparer.
              <a
                key={l.href}
                href={l.href}
                target="_blank"
                rel="noopener"
                className="rounded-md px-2 py-1.5 text-muted-foreground transition-colors hover:text-foreground"
              >
                {l.label}
              </a>
            ) : (
            <Link
              key={l.href}
              href={l.href}
              aria-current={isCurrent(l.href) ? "page" : undefined}
              className={cn(
                "rounded-md px-2 py-1.5 text-muted-foreground transition-colors hover:text-foreground",
                isCurrent(l.href) && "bg-muted font-medium text-foreground",
              )}
            >
              {l.label}
            </Link>
            ),
          )}
        </nav>
        <div className="ml-auto md:mt-auto md:ml-0">
          <Button variant="ghost" size="sm" onClick={logout} className="text-muted-foreground">
            Se déconnecter
          </Button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-4 py-5 md:px-8 md:py-6">
        <div className="mx-auto max-w-5xl">{children}</div>
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
    <div className="mx-auto mt-[16vh] w-full max-w-xs px-4">
      <h1 className="mb-6 text-lg font-semibold tracking-tight">efBlock</h1>
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
  );
}
