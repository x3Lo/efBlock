"use client";

import { useCallback, useEffect, useState } from "react";

import ConfirmButton from "@/components/confirm-button";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, DomainEntry, fmtDateTime, ListsResponse, nf } from "@/lib/api";

type Msg = { error: boolean; text: string } | null;

export default function Filters() {
  const [msg, setMsg] = useState<Msg>(null);
  const fail = useCallback((e: unknown) => setMsg({ error: true, text: e instanceof Error ? e.message : "Erreur" }), []);
  const info = useCallback((text: string) => setMsg({ error: false, text }), []);

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-lg font-semibold tracking-tight">Filtres</h1>
      {msg && (
        <p role={msg.error ? "alert" : "status"} className={`-mt-4 text-sm ${msg.error ? "text-destructive" : "text-muted-foreground"}`}>
          {msg.text}
        </p>
      )}
      <FilterLists onError={fail} onInfo={info} />
      <div className="grid gap-8 lg:grid-cols-2">
        <DomainList
          kind="allowlist"
          title="Allowlist"
          help="Jamais bloqués, sous-domaines compris, même s'ils figurent dans une liste."
          onError={fail}
        />
        <DomainList
          kind="blocklist"
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

  return (
    <section>
      <div className="mb-1 flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-semibold">Listes de filtres</h2>
        {data && <span className="text-xs text-muted-foreground">{nf.format(data.totalBlocked)} domaines au total</span>}
        <span className="flex-1" />
        <Button variant="outline" size="sm" disabled={busy} onClick={updateNow}>
          Mettre à jour maintenant
        </Button>
      </div>

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
                Aucune liste.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      <form className="mt-3 flex flex-wrap gap-2" onSubmit={add}>
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
      <p className="mt-2 text-xs text-muted-foreground">
        Formats acceptés : hosts (<code>0.0.0.0 domaine</code>), adblock (<code>||domaine^</code>), ou un domaine par ligne.
      </p>
    </section>
  );
}

function DomainList({
  kind,
  title,
  help,
  onError,
}: {
  kind: "allowlist" | "blocklist";
  title: string;
  help: string;
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
    <section>
      <h2 className="text-sm font-semibold">{title}</h2>
      <p className="mt-0.5 mb-3 text-xs text-muted-foreground">{help}</p>
      <form className="flex gap-2" onSubmit={add}>
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
    </section>
  );
}
