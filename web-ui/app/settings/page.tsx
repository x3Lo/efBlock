"use client";

import { useCallback, useEffect, useState } from "react";

import ConfirmButton from "@/components/confirm-button";
import Field from "@/components/field";
import ListField from "@/components/list-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, GroupName, Groups, SettingsView, UpstreamTestResult } from "@/lib/api";

type Msg = { error: boolean; text: string } | null;

export default function Settings() {
  const [view, setView] = useState<SettingsView | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api<SettingsView>("/api/settings")
      .then(setView)
      .catch((e) => setError(e instanceof Error ? e.message : "Erreur"));
  }, []);

  if (!view) return <p className="text-muted-foreground">{error || "Chargement…"}</p>;

  return (
    <div className="flex flex-col gap-10">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Réglages</h1>
        <p className="mt-1 max-w-2xl text-muted-foreground">
          Les changements sont appliqués tout de suite, sans redémarrage. Ils sont enregistrés à part et remplacent la
          valeur de <code>config.yaml</code> pour le groupe modifié ; « Rétablir » revient à <code>config.yaml</code>.
        </p>
      </div>
      <UpstreamSection view={view} setView={setView} />
      <LocalSection view={view} setView={setView} />
      <FilterSection view={view} setView={setView} />
      <CacheSection view={view} setView={setView} />
      <LogSection view={view} setView={setView} />
      <AccessSection view={view} setView={setView} />
      <PasswordSection />
      <InfraSection view={view} />
    </div>
  );
}

// --- Mécanique commune : un groupe de réglages = un brouillon + enregistrer / rétablir ---

type SectionProps = { view: SettingsView; setView: (v: SettingsView) => void };

function useGroup<G extends GroupName>(group: G, view: SettingsView, setView: (v: SettingsView) => void) {
  const effective = view.effective[group];
  const key = JSON.stringify(effective);
  const [draft, setDraft] = useState<Groups[G]>(effective);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<Msg>(null);

  // Quand la valeur en vigueur change (après un enregistrement ou un rétablissement), le brouillon la suit.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => setDraft(effective), [key]);

  const dirty = JSON.stringify(draft) !== key;
  const patch = (p: Partial<Groups[G]>) => setDraft((d) => ({ ...d, ...p }));

  async function run(method: "PUT" | "DELETE", ok: string) {
    setBusy(true);
    setMsg(null);
    try {
      setView(await api<SettingsView>(`/api/settings/${group}`, { method, body: method === "PUT" ? draft : undefined }));
      setMsg({ error: false, text: ok });
    } catch (e) {
      setMsg({ error: true, text: e instanceof Error ? e.message : "Erreur" });
    } finally {
      setBusy(false);
    }
  }

  return {
    draft,
    patch,
    dirty,
    busy,
    msg,
    overridden: view.overridden.includes(group),
    save: () => run("PUT", "Enregistré et appliqué."),
    reset: () => run("DELETE", "Valeurs de config.yaml rétablies."),
  };
}

type GroupState = ReturnType<typeof useGroup>;

function Section({
  title,
  help,
  g,
  children,
  actions,
  below,
}: {
  title: string;
  help?: string;
  g: Pick<GroupState, "dirty" | "busy" | "msg" | "overridden" | "save" | "reset">;
  children: React.ReactNode;
  actions?: React.ReactNode;
  below?: React.ReactNode;
}) {
  return (
    <section className="max-w-2xl border-t pt-5">
      <div className="mb-3 flex items-center gap-2">
        <h2 className="text-sm font-semibold">{title}</h2>
        {g.overridden && <Badge>modifié depuis l&apos;interface</Badge>}
      </div>
      {help && <p className="mb-4 text-muted-foreground">{help}</p>}
      <div className="flex flex-col gap-4">{children}</div>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button size="sm" disabled={!g.dirty || g.busy} onClick={g.save}>
          Enregistrer
        </Button>
        {actions}
        {g.overridden && (
          <Button variant="ghost" size="sm" disabled={g.busy} onClick={g.reset}>
            Rétablir config.yaml
          </Button>
        )}
        {g.msg && (
          <span role={g.msg.error ? "alert" : "status"} className={`text-sm ${g.msg.error ? "text-destructive" : "text-muted-foreground"}`}>
            {g.msg.text}
          </span>
        )}
      </div>
      {below}
    </section>
  );
}

// --- Sections ------------------------------------------------------------------

function UpstreamSection({ view, setView }: SectionProps) {
  const g = useGroup("upstream", view, setView);
  const [results, setResults] = useState<UpstreamTestResult[] | null>(null);
  const [testing, setTesting] = useState(false);
  const [testError, setTestError] = useState("");

  async function test() {
    setTesting(true);
    setTestError("");
    setResults(null);
    try {
      const r = await api<{ results: UpstreamTestResult[] }>("/api/upstreams/test", {
        method: "POST",
        body: { upstreams: g.draft.upstreams, bootstrap: g.draft.bootstrap, timeout: g.draft.timeout },
      });
      setResults(r.results);
    } catch (e) {
      setTestError(e instanceof Error ? e.message : "Erreur");
    } finally {
      setTesting(false);
    }
  }

  return (
    <Section
      title="Résolveurs amont"
      help="Les serveurs DNS auxquels efBlock envoie les requêtes qu'il ne bloque pas, essayés dans l'ordre. Utilise « Tester » avant d'enregistrer : un réseau qui bloque le port 853 se voit tout de suite."
      g={g}
      actions={
        <Button variant="outline" size="sm" disabled={testing || g.draft.upstreams.length === 0} onClick={test}>
          {testing ? "Test en cours…" : "Tester"}
        </Button>
      }
      below={
        (results || testError) && (
          <div className="mt-3 text-xs">
            {testError && <p className="text-destructive">{testError}</p>}
            {results && (
              <ul className="flex flex-col gap-1">
                {results.map((r) => (
                  <li key={r.name} className="flex flex-wrap items-center gap-2">
                    <Badge variant={r.ok ? "ok" : "destructive"}>{r.ok ? "ok" : "échec"}</Badge>
                    <span className="font-mono">{r.name}</span>
                    <span className="text-muted-foreground">{r.ok ? `${r.latencyMs} ms` : r.error}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )
      }
    >
      <Field label="Upstreams, un par ligne" help="tls://1.1.1.1 (DoT, port 853), https://cloudflare-dns.com/dns-query (DoH, port 443), ou 1.1.1.1 (non chiffré).">
        <ListField label="Upstreams" rows={4} value={g.draft.upstreams} onChange={(v) => g.patch({ upstreams: v })} placeholder={"tls://1.1.1.1\ntls://9.9.9.9"} />
      </Field>
      <Field label="Serveurs de démarrage (bootstrap)" help="DNS classiques, utilisés seulement pour trouver l'adresse d'un upstream donné par son nom.">
        <ListField label="Bootstrap" rows={2} value={g.draft.bootstrap} onChange={(v) => g.patch({ bootstrap: v })} placeholder="1.1.1.1:53" />
      </Field>
      <Field label="Délai maximal par upstream" help="Exemples : 2s, 3s, 500ms.">
        <Input className="w-32" value={g.draft.timeout} onChange={(e) => g.patch({ timeout: e.target.value })} />
      </Field>
    </Section>
  );
}

function LocalSection({ view, setView }: SectionProps) {
  const g = useGroup("local", view, setView);
  return (
    <Section
      title="Réseau local"
      help="Pour que les noms de ta maison (nas.maison, imprimante.lan) et la recherche inverse des adresses privées ne partent jamais sur internet. Les adresses du type 192.168.x.x sont déjà traitées localement par défaut."
      g={g}
    >
      <Field label="Résolveur du réseau local" help="Souvent ta box ou ton routeur, par exemple 192.168.1.1. Il sert aussi à afficher le nom des machines dans le journal.">
        <Input className="w-56" value={g.draft.upstream} placeholder="192.168.1.1" onChange={(e) => g.patch({ upstream: e.target.value })} />
      </Field>
      <Field label="Domaines locaux, un par ligne" help="Les noms qui finissent par ces suffixes sont envoyés au résolveur local et jamais à internet. Exemple : lan, maison.">
        <ListField label="Domaines locaux" rows={2} value={g.draft.domains} onChange={(v) => g.patch({ domains: v })} placeholder="lan" />
      </Field>
      <Field label="Noms fixes, un par ligne" help="Même format que le fichier /etc/hosts : l'adresse, puis un ou plusieurs noms.">
        <ListField label="Noms fixes" rows={4} value={g.draft.hosts} onChange={(v) => g.patch({ hosts: v })} placeholder={"192.168.1.20 nas.maison nas\n192.168.1.1 box.maison"} />
      </Field>
    </Section>
  );
}

function FilterSection({ view, setView }: SectionProps) {
  const g = useGroup("filter", view, setView);
  return (
    <Section title="Blocage" g={g}>
      <Field label="Réponse pour un domaine bloqué" help="0.0.0.0 fait échouer la connexion tout de suite. NXDOMAIN répond que le domaine n'existe pas.">
        <Select className="w-72" value={g.draft.block_mode} onChange={(e) => g.patch({ block_mode: e.target.value })}>
          <option value="zero_ip">0.0.0.0 et :: (par défaut)</option>
          <option value="nxdomain">NXDOMAIN</option>
        </Select>
      </Field>
      <Field label="Mise à jour des listes toutes les" help="Exemples : 12h, 24h, 30m. Pris en compte au prochain cycle.">
        <Input className="w-32" value={g.draft.update_interval} onChange={(e) => g.patch({ update_interval: e.target.value })} />
      </Field>
      <div className="flex flex-col gap-1">
        <label className="flex items-center gap-2">
          <input type="checkbox" className="size-4 accent-primary" checked={g.draft.cname_check ?? true} onChange={(e) => g.patch({ cname_check: e.target.checked })} />
          <span>Vérifier aussi les alias (CNAME)</span>
        </label>
        <span className="text-xs text-muted-foreground">
          Un traqueur peut se cacher derrière un nom innocent qui n&apos;est qu&apos;un alias vers lui. Avec cette option, la réponse est bloquée si
          l&apos;un des alias mène à un domaine de tes listes. Un domaine que tu as toi-même autorisé n&apos;est jamais bloqué de cette façon.
        </span>
      </div>
    </Section>
  );
}

function CacheSection({ view, setView }: SectionProps) {
  const g = useGroup("cache", view, setView);
  return (
    <Section title="Cache DNS" help="Modifier le cache le vide." g={g}>
      <label className="flex items-center gap-2">
        <input type="checkbox" className="size-4 accent-primary" checked={g.draft.enabled} onChange={(e) => g.patch({ enabled: e.target.checked })} />
        <span>Activer le cache</span>
      </label>
      <Field label="Nombre maximal de réponses gardées" help="Environ 1 Ko chacune.">
        <Input type="number" min={1} className="w-32" value={g.draft.max_entries} onChange={(e) => g.patch({ max_entries: Number(e.target.value) })} />
      </Field>
      <Field label="Durée de vie maximale" help="Plafond appliqué même si le serveur annonce plus long. Exemple : 24h.">
        <Input className="w-32" value={g.draft.max_ttl} onChange={(e) => g.patch({ max_ttl: e.target.value })} />
      </Field>
      <div className="flex flex-col gap-1">
        <label className="flex items-center gap-2">
          <input type="checkbox" className="size-4 accent-primary" checked={g.draft.serve_stale ?? true} onChange={(e) => g.patch({ serve_stale: e.target.checked })} />
          <span>Servir une ancienne réponse si aucun upstream ne répond</span>
        </label>
        <span className="text-xs text-muted-foreground">
          Si tes upstreams tombent, un site déjà visité reste joignable avec son ancienne adresse au lieu de tomber en erreur.
        </span>
      </div>
      <Field label="Âge maximal d'une réponse de secours" help="Au-delà, elle n'est plus utilisée. Exemple : 24h.">
        <Input className="w-32" value={g.draft.stale_max ?? "24h"} disabled={g.draft.serve_stale === false} onChange={(e) => g.patch({ stale_max: e.target.value })} />
      </Field>
      <div className="flex flex-col gap-1">
        <label className="flex items-center gap-2">
          <input type="checkbox" className="size-4 accent-primary" checked={g.draft.prefetch ?? true} onChange={(e) => g.patch({ prefetch: e.target.checked })} />
          <span>Rafraîchir en avance les réponses populaires</span>
        </label>
        <span className="text-xs text-muted-foreground">Une réponse très demandée est renouvelée juste avant d&apos;expirer, pour ne jamais faire attendre.</span>
      </div>
    </Section>
  );
}

function LogSection({ view, setView }: SectionProps) {
  const g = useGroup("querylog", view, setView);
  return (
    <Section
      title="Journal des requêtes"
      help={
        view.infra.querylog_enabled
          ? "Les anciennes lignes sont supprimées automatiquement."
          : "Le journal est désactivé dans config.yaml (querylog.enabled: false) : ce réglage n'a aucun effet."
      }
      g={g}
    >
      <Field label="Durée de conservation, en jours" help="-1 pour garder sans limite.">
        <Input type="number" min={-1} className="w-32" value={g.draft.retention_days} onChange={(e) => g.patch({ retention_days: Number(e.target.value) })} />
      </Field>
    </Section>
  );
}

function AccessSection({ view, setView }: SectionProps) {
  const g = useGroup("access", view, setView);
  return (
    <Section title="Accès" help="Qui peut utiliser le DNS et qui peut ouvrir cette interface. Un réseau s'écrit 192.168.1.0/24 ; une seule machine, 192.168.1.50/32." g={g}>
      <Field label="Clients autorisés à utiliser le DNS" help="Toute autre adresse reçoit un refus. Par défaut : les réseaux privés.">
        <ListField label="Clients DNS autorisés" rows={4} value={g.draft.dns_clients} onChange={(v) => g.patch({ dns_clients: v })} />
      </Field>
      <Field label="Réseaux autorisés à ouvrir cette interface" help="Ton adresse actuelle doit en faire partie : sinon l'enregistrement est refusé, pour ne pas te couper l'accès.">
        <ListField label="Réseaux autorisés pour l'interface" rows={4} value={g.draft.web_cidrs} onChange={(v) => g.patch({ web_cidrs: v })} />
      </Field>
    </Section>
  );
}

function PasswordSection() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<Msg>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (next !== again) return setMsg({ error: true, text: "Les deux nouveaux mots de passe sont différents." });
    setBusy(true);
    setMsg(null);
    try {
      await api("/api/password", { method: "POST", body: { current, new: next } });
      setCurrent("");
      setNext("");
      setAgain("");
      setMsg({ error: false, text: "Mot de passe changé. Les autres sessions ouvertes ont été fermées." });
    } catch (err) {
      setMsg({ error: true, text: err instanceof Error ? err.message : "Erreur" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="max-w-2xl border-t pt-5">
      <h2 className="mb-3 text-sm font-semibold">Mot de passe</h2>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field label="Mot de passe actuel">
          <Input type="password" autoComplete="current-password" className="w-64" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field label="Nouveau mot de passe" help="8 caractères au moins.">
          <Input type="password" autoComplete="new-password" className="w-64" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <Field label="Confirmer le nouveau mot de passe">
          <Input type="password" autoComplete="new-password" className="w-64" value={again} onChange={(e) => setAgain(e.target.value)} />
        </Field>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={busy || !current || !next || !again}>
            Changer le mot de passe
          </Button>
          {msg && (
            <span role={msg.error ? "alert" : "status"} className={`text-sm ${msg.error ? "text-destructive" : "text-muted-foreground"}`}>
              {msg.text}
            </span>
          )}
        </div>
      </form>
    </section>
  );
}

// Réglages qui ne se changent que dans config.yaml : on les affiche pour information.
function InfraSection({ view }: { view: SettingsView }) {
  const [state, setState] = useState<"idle" | "restarting" | "error">("idle");
  const [error, setError] = useState("");
  const i = view.infra;

  const restart = useCallback(async () => {
    setState("restarting");
    try {
      await api("/api/restart", { method: "POST" });
      // Le service redémarre : on attend qu'il réponde de nouveau, puis on recharge la page.
      const started = Date.now();
      const poll = async () => {
        try {
          await fetch("/api/session", { cache: "no-store" });
          if (Date.now() - started > 2000) return location.reload();
        } catch {
          /* le serveur est arrêté : on réessaie */
        }
        if (Date.now() - started > 60_000) return setState("error");
        setTimeout(poll, 1000);
      };
      setTimeout(poll, 1500);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Erreur");
      setState("error");
    }
  }, []);

  const rows: [string, string][] = [
    ["Écoute DNS", i.dns_listen],
    ["Écoute de l'interface", `${i.web_listen} (${i.https ? "HTTPS" : "HTTP"})`],
    ["Dossier de données", i.data_dir],
    ["Journal des requêtes", i.querylog_enabled ? "activé" : "désactivé"],
  ];

  return (
    <section className="max-w-2xl border-t pt-5">
      <h2 className="mb-1 text-sm font-semibold">Infrastructure</h2>
      <p className="mb-3 text-muted-foreground">
        Ces réglages se changent seulement dans <code>/etc/efblock/config.yaml</code>, puis un redémarrage : une erreur
        d&apos;adresse d&apos;écoute te couperait l&apos;accès à cette interface, il vaut mieux pouvoir la réparer depuis un terminal.
      </p>
      <dl className="mb-4 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="font-mono text-xs">{v}</dd>
          </div>
        ))}
      </dl>
      {view.canRestart ? (
        <div className="flex flex-wrap items-center gap-3">
          {state === "restarting" ? (
            <span className="text-muted-foreground">Redémarrage en cours, la page se rechargera toute seule…</span>
          ) : (
            <ConfirmButton label="Redémarrer efBlock" onConfirm={restart} />
          )}
          {state === "error" && <span className="text-sm text-destructive">{error || "Le service n'a pas répondu après 60 secondes."}</span>}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">Le redémarrage depuis l&apos;interface n&apos;est disponible que lorsqu&apos;efBlock tourne comme service systemd.</p>
      )}
    </section>
  );
}
