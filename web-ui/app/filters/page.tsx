"use client";

import { Check, ListFilter, Plus, RefreshCw, ShieldBan, ShieldCheck, Sparkles } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import ConfirmButton from "@/components/confirm-button";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, DomainEntry, fmtDateTime, ListsResponse, nf } from "@/lib/api";
import { recommendedLists } from "@/lib/recommended-lists";

type Msg = { error: boolean; text: string } | null;

export default function Filters() {
  const [msg, setMsg] = useState<Msg>(null);
  const fail = useCallback((e: unknown) => setMsg({ error: true, text: e instanceof Error ? e.message : "Erreur" }), []);
  const info = useCallback((text: string) => setMsg({ error: false, text }), []);

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Filtres</h1>
        <p className="mt-1 text-muted-foreground">Ce qu&apos;efBlock bloque : des listes publiques mises à jour automatiquement, plus tes propres règles.</p>
      </div>
      {msg && (
        <p role={msg.error ? "alert" : "status"} className={`text-sm ${msg.error ? "text-destructive" : "text-muted-foreground"}`}>
          {msg.text}
        </p>
      )}
      <FilterLists onError={fail} onInfo={info} />
      <div className="grid gap-4 lg:grid-cols-2">
        <DomainList
          kind="allowlist"
          icon={ShieldCheck}
          title="Allowlist"
          help="Jamais bloqués, sous-domaines compris, même s'ils figurent dans une liste. Utile si un site ou un jeu ne marche plus."
          onError={fail}
        />
        <DomainList
          kind="blocklist"
          icon={ShieldBan}
          title="Blocklist"
          help="Toujours bloqués, sous-domaines compris, en plus des listes."
          onError={fail}
        />
      </div>
    </div>
  );
}

function FilterLists({ onError, onInfo }: { onError: (e: unknown) => void; onInfo: (t: string) => void }) {
  const [data, setData] = useState<ListsResponse | null>(null);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setData(await api<ListsResponse>("/api/lists"));
    } catch (e) {
      onError(e);
    }
  }, [onError]);

  useEffect(() => {
    load();
  }, [load]);

  // Le téléchargement est asynchrone : on recharge le statut quelques fois.
  const refreshSoon = () => [1500, 5000, 12000].forEach((ms) => setTimeout(load, ms));

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      setData(await api<ListsResponse>("/api/lists", { method: "POST", body: { name, url } }));
      setName("");
      setUrl("");
      onInfo("Liste ajoutée, téléchargement en cours.");
      refreshSoon();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function addRecommended(n: string, u: string) {
    setBusy(true);
    try {
      setData(await api<ListsResponse>("/api/lists", { method: "POST", body: { name: n, url: u } }));
      onInfo(`${n} ajoutée, téléchargement en cours.`);
      refreshSoon();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function remove(u: string) {
    try {
      setData(await api<ListsResponse>(`/api/lists?url=${encodeURIComponent(u)}`, { method: "DELETE" }));
      refreshSoon();
    } catch (err) {
      onError(err);
    }
  }

  async function updateNow() {
    setBusy(true);
    try {
      await api("/api/lists/update", { method: "POST" });
      onInfo("Mise à jour des listes lancée.");
      refreshSoon();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  const added = new Set(data?.lists.map((l) => l.url) ?? []);

  return (
    <>
    <Card>
      <CardHeader title="Listes recommandées" icon={Sparkles} />
      <p className="px-4 pb-3 text-xs text-muted-foreground">
        Ajoute une liste en un clic. Une seule suffit pour commencer ; en ajouter plusieurs bloque davantage mais peut gêner certains sites.
      </p>
      <div className="grid gap-3 px-4 pb-4 sm:grid-cols-2 xl:grid-cols-3">
        {recommendedLists.map((r) => {
          const isAdded = added.has(r.url);
          return (
            <div key={r.url} className={`flex flex-col gap-2 rounded-lg border p-3 ${isAdded ? "border-ok/40 bg-ok/5" : "bg-background/40"}`}>
              <div className="flex items-center gap-2">
                <span className="font-medium">{r.name}</span>
                {r.recommended && <Badge variant="ok">conseillée</Badge>}
              </div>
              <p className="flex-1 text-xs text-muted-foreground">{r.description}</p>
              {isAdded ? (
                <span className="flex items-center gap-1.5 text-xs font-medium text-ok">
                  <Check className="size-3.5" aria-hidden /> Ajoutée
                </span>
              ) : (
                <Button size="sm" variant="outline" disabled={busy} onClick={() => addRecommended(r.name, r.url)} className="self-start">
                  <Plus className="size-3.5" aria-hidden /> Ajouter
                </Button>
              )}
            </div>
          );
        })}
      </div>
    </Card>

    <Card>
      <CardHeader
        title={
          <>
            Tes listes
            {data && <span className="ml-1 text-xs font-normal text-muted-foreground">{nf.format(data.totalBlocked)} domaines au total</span>}
          </>
        }
        icon={ListFilter}
        actions={
          <Button variant="outline" size="sm" disabled={busy} onClick={updateNow}>
            <RefreshCw className="size-3.5" aria-hidden /> Mettre à jour maintenant
          </Button>
        }
      />
      <div className="px-2">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Liste</TableHead>
            <TableHead className="text-right">Domaines</TableHead>
            <TableHead>Mise à jour</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {data?.lists.map((l) => (
            <TableRow key={l.url}>
              <TableCell>
                <div className="font-medium">
                  {l.name} {l.source === "config" && <span className="text-xs font-normal text-muted-foreground">(config.yaml)</span>}
                </div>
                <div className="font-mono text-xs break-all text-muted-foreground">{l.url}</div>
                {l.error && <div className="text-xs text-destructive">Erreur : {l.error}</div>}
              </TableCell>
              <TableCell className="text-right font-mono text-xs tabular-nums">{nf.format(l.blocked)}</TableCell>
              <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                {l.updatedAt ? fmtDateTime(l.updatedAt) : "pas encore téléchargée"}
              </TableCell>
              <TableCell className="text-right">
                {l.source === "ui" && <ConfirmButton ariaLabel={`Retirer la liste ${l.name}`} onConfirm={() => remove(l.url)} />}
              </TableCell>
            </TableRow>
          ))}
          {data && data.lists.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="py-4 text-muted-foreground">
                Aucune liste pour l&apos;instant : choisis-en une parmi les listes recommandées ci-dessus.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      </div>
      <form className="mt-1 flex flex-wrap gap-2 px-4" onSubmit={add}>
        <Input
          type="text"
          placeholder="Nom (facultatif)"
          aria-label="Nom de la liste"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-44 flex-none"
        />
        <Input
          type="text"
          placeholder="https://exemple.org/hosts.txt"
          aria-label="Adresse de la liste"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          className="min-w-56 flex-1"
          required
        />
        <Button disabled={busy || !url}>Ajouter</Button>
      </form>
      <p className="mt-2 px-4 pb-4 text-xs text-muted-foreground">
        Autre liste : colle son adresse. Formats acceptés : hosts (<code>0.0.0.0 domaine</code>), adblock (<code>||domaine^</code>), ou un domaine par ligne.
      </p>
    </Card>
    </>
  );
}

function DomainList({
  kind,
  title,
  help,
  icon,
  onError,
}: {
  kind: "allowlist" | "blocklist";
  title: string;
  help: string;
  icon: React.ComponentType<{ className?: string }>;
  onError: (e: unknown) => void;
}) {
  const [entries, setEntries] = useState<DomainEntry[]>([]);
  const [domain, setDomain] = useState("");
  const [busy, setBusy] = useState(false);
  const path = `/api/${kind}`;

  useEffect(() => {
    api<{ entries: DomainEntry[] }>(path)
      .then((r) => setEntries(r.entries))
      .catch(onError);
  }, [path, onError]);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      setEntries((await api<{ entries: DomainEntry[] }>(path, { method: "POST", body: { domain } })).entries);
      setDomain("");
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  async function remove(d: string) {
    try {
      setEntries((await api<{ entries: DomainEntry[] }>(`${path}?domain=${encodeURIComponent(d)}`, { method: "DELETE" })).entries);
    } catch (err) {
      onError(err);
    }
  }

  return (
    <Card>
      <CardHeader title={title} icon={icon} />
      <p className="px-4 pb-3 text-xs text-muted-foreground">{help}</p>
      <form className="flex gap-2 px-4" onSubmit={add}>
        <Input
          type="text"
          placeholder="exemple.com"
          aria-label={`Domaine à ajouter à la ${title.toLowerCase()}`}
          value={domain}
          onChange={(e) => setDomain(e.target.value)}
          autoCapitalize="off"
          spellCheck={false}
        />
        <Button disabled={busy || !domain.trim()}>Ajouter</Button>
      </form>
      <div className="px-2 pb-2">
      <Table className="mt-2">
        <TableBody>
          {entries.map((e) => (
            <TableRow key={e.domain}>
              <TableCell className="font-mono text-xs break-all">{e.domain}</TableCell>
              <TableCell className="text-right">
                {e.source === "config" ? (
                  <span className="text-xs text-muted-foreground">config.yaml</span>
                ) : (
                  <ConfirmButton ariaLabel={`Retirer ${e.domain}`} onConfirm={() => remove(e.domain)} />
                )}
              </TableCell>
            </TableRow>
          ))}
          {entries.length === 0 && (
            <TableRow>
              <TableCell className="py-3 text-muted-foreground">Aucun domaine.</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      </div>
    </Card>
  );
}
