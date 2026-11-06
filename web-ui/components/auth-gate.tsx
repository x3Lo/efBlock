"use client";

import { ChevronDown, LayoutDashboard, ListFilter, LogOut, Menu, ScrollText, Settings, ShieldCheck, Users, X } from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";

import StatusIndicators from "@/components/status-indicators";
import ThemeToggle from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, UNAUTHORIZED_EVENT } from "@/lib/api";
import { sectionFromHash, settingsSections, useHash } from "@/lib/settings-sections";
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
  { href: "/settings/", label: "Réglages", icon: Settings, children: settingsSections },
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
  const [drawer, setDrawer] = useState(false);

  // Le menu mobile se referme à chaque changement de page, et avec la touche Échap.
  useEffect(() => setDrawer(false), [path]);
  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setDrawer(false);
    document.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden"; // la page ne défile pas sous le menu ouvert
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [drawer]);

  const logout = useCallback(async () => {
    try {
      await api("/api/logout", { method: "POST" });
    } finally {
      onLogout();
    }
  }, [onLogout]);

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      {/* Ordinateur : barre latérale fixe à gauche. */}
      <aside className="sticky top-0 hidden h-screen w-56 shrink-0 flex-col gap-3 border-r bg-card px-3 py-5 md:flex">
        <Brand />
        <SideMenu path={path} onLogout={logout} onNavigate={() => {}} />
      </aside>

      {/* Téléphone : barre du haut avec un bouton menu, qui ouvre un panneau à droite. */}
      <header className="sticky top-0 z-20 flex items-center justify-between gap-3 border-b bg-card/95 px-3 py-2.5 backdrop-blur md:hidden">
        <Brand />
        <Button variant="ghost" size="icon" onClick={() => setDrawer(true)} aria-label="Ouvrir le menu" aria-expanded={drawer} aria-controls="menu-mobile">
          <Menu className="size-5" aria-hidden />
        </Button>
      </header>
      <div
        aria-hidden={!drawer}
        onClick={() => setDrawer(false)}
        className={cn("fixed inset-0 z-30 bg-black/50 transition-opacity md:hidden", drawer ? "opacity-100" : "pointer-events-none opacity-0")}
      />
      <aside
        id="menu-mobile"
        role="dialog"
        aria-modal="true"
        aria-label="Menu"
        inert={!drawer}
        className={cn(
          "fixed inset-y-0 right-0 z-40 flex w-72 max-w-[85vw] flex-col gap-3 border-l bg-card px-3 py-4 shadow-xl transition-transform duration-200 md:hidden",
          drawer ? "translate-x-0" : "translate-x-full",
        )}
      >
        <div className="flex items-center justify-between">
          <Brand />
          <Button variant="ghost" size="icon" onClick={() => setDrawer(false)} aria-label="Fermer le menu">
            <X className="size-5" aria-hidden />
          </Button>
        </div>
        <SideMenu path={path} onLogout={logout} onNavigate={() => setDrawer(false)} />
      </aside>

      <main className="min-w-0 flex-1 px-4 py-5 md:px-8 md:py-7">
        <div className="mx-auto max-w-6xl">{children}</div>
      </main>
    </div>
  );
}

// Contenu du menu, identique dans la barre latérale (ordinateur) et le panneau (téléphone).
function SideMenu({ path, onLogout, onNavigate }: { path: string; onLogout: () => void; onNavigate: () => void }) {
  const router = useRouter();
  const hash = useHash(path);
  const [openSettings, setOpenSettings] = useState(false);
  const isCurrent = (href: string) => (href === "/" ? path === "/" : path.startsWith(href.replace(/\/$/, "")));

  // Aller à une section des Réglages : sur la page, on change seulement le # (l'affichage suit) ;
  // ailleurs, on ouvre la page sur la bonne section.
  const goSection = (id: string) => {
    if (path.startsWith("/settings")) window.location.hash = id;
    else router.push(`/settings/#${id}`);
    onNavigate();
  };

  return (
    <>
      <nav aria-label="Navigation principale" className="mt-3 flex flex-col gap-1">
        {links.map((l) => {
          const Icon = l.icon;
          const current = isCurrent(l.href);
          const item = cn(
            "flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
            current && "bg-primary/15 font-medium text-primary hover:bg-primary/20 hover:text-primary",
          );
          if (!l.children) {
            return (
              <Link key={l.href} href={l.href} aria-current={current ? "page" : undefined} className={item} onClick={onNavigate}>
                <Icon className="size-4 shrink-0" aria-hidden />
                {l.label}
              </Link>
            );
          }
          // Entrée dépliante : « Réglages » et ses sections.
          const open = current || openSettings;
          const active = sectionFromHash(hash);
          return (
            <div key={l.href} className="flex flex-col gap-1">
              <div className="flex items-center">
                <Link href={l.href} aria-current={current ? "page" : undefined} className={cn(item, "flex-1")} onClick={onNavigate}>
                  <Icon className="size-4 shrink-0" aria-hidden />
                  {l.label}
                </Link>
                <button
                  type="button"
                  aria-expanded={open}
                  aria-label={open ? "Replier les réglages" : "Déplier les réglages"}
                  onClick={() => setOpenSettings(!open)}
                  className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} aria-hidden />
                </button>
              </div>
              {open && (
                <ul className="ml-4 flex flex-col gap-1 border-l pl-2">
                  {l.children.map((c) => {
                    const CIcon = c.icon;
                    const on = current && active === c.id;
                    return (
                      <li key={c.id}>
                        <a
                          href={`/settings/#${c.id}`}
                          onClick={(e) => {
                            e.preventDefault();
                            goSection(c.id);
                          }}
                          aria-current={on ? "location" : undefined}
                          className={cn(
                            "flex items-center gap-2 rounded-md px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                            on && "font-medium text-primary hover:text-primary",
                          )}
                        >
                          <CIcon className="size-3.5 shrink-0" aria-hidden />
                          {c.label}
                        </a>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>
          );
        })}
      </nav>
      <div className="mt-auto flex flex-col gap-2">
        <StatusIndicators />
        <div className="flex flex-col">
          <ThemeToggle />
          <Button variant="ghost" size="sm" onClick={onLogout} className="justify-start gap-2 text-muted-foreground">
            <LogOut className="size-4" aria-hidden />
            Se déconnecter
          </Button>
        </div>
      </div>
    </>
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
