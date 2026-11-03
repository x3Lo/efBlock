"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { api, UNAUTHORIZED_EVENT } from "../lib/api";

type State = "loading" | "in" | "out";

// AuthGate : n'affiche l'application que si l'utilisateur est connecté ;
// sinon, la page de connexion. Les DONNÉES sont protégées par le serveur
// (l'API répond 401 sans session) : ce composant ne fait que l'affichage.
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

  if (state === "loading") return <p className="center">Chargement…</p>;
  if (state === "out") return <Login onSuccess={() => setState("in")} />;
  return (
    <>
      <TopBar onLogout={() => setState("out")} />
      <main>{children}</main>
    </>
  );
}

function TopBar({ onLogout }: { onLogout: () => void }) {
  const path = usePathname();
  const logout = useCallback(async () => {
    try {
      await api("/api/logout", { method: "POST" });
    } finally {
      onLogout();
    }
  }, [onLogout]);

  const links = [
    { href: "/", label: "Tableau de bord" },
    { href: "/queries/", label: "Journal" },
    { href: "/filters/", label: "Filtres" },
  ];
  const current = (href: string) => (href === "/" ? path === "/" : path.startsWith(href.replace(/\/$/, "")));

  return (
    <header className="top">
      <div className="in">
        <span className="brand">🛡️ efBlock</span>
        <nav aria-label="Navigation principale">
          {links.map((l) => (
            <Link key={l.href} href={l.href} aria-current={current(l.href) ? "page" : undefined}>
              {l.label}
            </Link>
          ))}
        </nav>
        <button onClick={logout}>Se déconnecter</button>
      </div>
    </header>
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
    <div className="login card">
      <h1>🛡️ efBlock</h1>
      <form onSubmit={submit}>
        <label>
          Mot de passe
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            autoFocus
            required
          />
        </label>
        {error && <div className="notice error" role="alert">{error}</div>}
        <button className="primary" disabled={busy || !password}>
          {busy ? "Connexion…" : "Se connecter"}
        </button>
      </form>
    </div>
  );
}
