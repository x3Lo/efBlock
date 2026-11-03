"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, fmtTime, QueryEntry, UNAUTHORIZED_EVENT } from "@/lib/api";

// Une ligne affichée : l'entrée du serveur + un identifiant local stable (clé React).
type Row = QueryEntry & { uid: number };

const MAX_ROWS = 300;

const RESULT: Record<QueryEntry["action"], { label: string; variant: "default" | "blocked" | "destructive" | "ok" }> = {
  blocked: { label: "bloquée", variant: "blocked" },
  cached: { label: "cache", variant: "default" },
  forwarded: { label: "transmise", variant: "default" },
  local: { label: "local", variant: "ok" },
  error: { label: "échec", variant: "destructive" },
};

export default function Queries() {
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [logEnabled, setLogEnabled] = useState(true);
  const [paused, setPaused] = useState(false);
  const [live, setLive] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [message, setMessage] = useState<{ error: boolean; text: string } | null>(null);

  const uid = useRef(0);
  const pending = useRef<Row[]>([]); // entrées reçues, pas encore affichées
  const filterRef = useRef("");
  filterRef.current = filter.toLowerCase();

  // On attend que la saisie se calme avant de filtrer.
  useEffect(() => {
    const id = setTimeout(() => setFilter(search.trim()), 250);
    return () => clearTimeout(id);
  }, [search]);

  // Historique : les dernières entrées (filtrées côté serveur).
  const loadHistory = useCallback(async () => {
    try {
      const r = await api<{ logEnabled: boolean; entries: QueryEntry[] }>(
        `/api/queries?limit=200&q=${encodeURIComponent(filter)}`,
      );
      pending.current = [];
      setRows(r.entries.map((e) => ({ ...e, uid: uid.current++ })));
      setLogEnabled(r.logEnabled);
    } catch (e) {
      setMessage({ error: true, text: e instanceof Error ? e.message : "Erreur" });
    } finally {
      setLoaded(true);
    }
  }, [filter]);

  useEffect(() => {
    loadHistory();
  }, [loadHistory]);

  // Flux en direct (Server-Sent Events). Le navigateur se reconnecte seul après une coupure.
  useEffect(() => {
    if (paused || !logEnabled) return;
    const es = new EventSource("/api/queries/stream");
    es.onopen = () => setLive(true);
    es.onerror = () => {
      setLive(false);
      // Si le serveur a refusé (session expirée), EventSource abandonne : on le détecte.
      if (es.readyState === EventSource.CLOSED) api("/api/session").catch(() => window.dispatchEvent(new Event(UNAUTHORIZED_EVENT)));
    };
    es.onmessage = (ev) => {
      const e = JSON.parse(ev.data) as QueryEntry;
      const f = filterRef.current;
      if (f && !e.name.toLowerCase().includes(f) && !e.client.toLowerCase().includes(f) && !(e.clientName ?? "").toLowerCase().includes(f)) return;
      pending.current.push({ ...e, uid: uid.current++ });
    };
    // Sur un réseau chargé, on regroupe les arrivées : un affichage toutes les 250 ms.
    const flush = setInterval(() => {
      if (pending.current.length === 0) return;
      const batch = pending.current.reverse(); // la plus récente en premier
      pending.current = [];
      setRows((r) => [...batch, ...r].slice(0, MAX_ROWS));
    }, 250);
    return () => {
      es.close();
      clearInterval(flush);
      setLive(false);
    };
  }, [paused, logEnabled]);

  async function act(e: Row) {
    const toAllow = e.action === "blocked";
    try {
      await api(toAllow ? "/api/allowlist" : "/api/blocklist", { method: "POST", body: { domain: e.name } });
      setMessage({ error: false, text: `${e.name} : ${toAllow ? "ajouté à l'allowlist" : "ajouté à la blocklist"}.` });
    } catch (err) {
      setMessage({ error: true, text: err instanceof Error ? err.message : "Erreur" });
    }
  }

  function resume() {
    setPaused(false);
    loadHistory(); // récupère ce qui s'est passé pendant la pause
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Journal</h1>
        <div className="flex items-center gap-3">
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground" role="status">
            <span
              aria-hidden
              className={`size-2 rounded-full ${live ? "bg-ok" : paused ? "bg-warn" : "bg-muted-foreground/40"}`}
            />
            {live ? "En direct" : paused ? "En pause" : "Hors ligne"}
          </span>
          {paused ? (
            <Button size="sm" onClick={resume}>
              Reprendre
            </Button>
          ) : (
            <Button variant="outline" size="sm" onClick={() => setPaused(true)}>
              Mettre en pause
            </Button>
          )}
        </div>
      </div>

      <Input
        type="search"
        placeholder="Filtrer par domaine ou par client"
        aria-label="Filtrer le journal"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="max-w-sm"
      />

      {message && (
        <p role={message.error ? "alert" : "status"} className={`text-sm ${message.error ? "text-destructive" : "text-muted-foreground"}`}>
          {message.text}
        </p>
      )}

      {!logEnabled ? (
        <p className="text-muted-foreground">Le journal des requêtes est désactivé.</p>
      ) : !loaded ? (
        <p className="text-muted-foreground">Chargement…</p>
      ) : rows.length === 0 ? (
        <p className="py-6 text-muted-foreground">
          {filter ? "Aucune requête pour ce filtre." : "Aucune requête enregistrée pour l'instant."}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Heure</TableHead>
              <TableHead>Client</TableHead>
              <TableHead>Domaine</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Résultat</TableHead>
              <TableHead className="text-right">Durée</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((e) => (
              <TableRow key={e.uid}>
                <TableCell className="font-mono text-xs whitespace-nowrap text-muted-foreground">{fmtTime(e.time)}</TableCell>
                <TableCell className="font-mono text-xs whitespace-nowrap">
                  {e.clientName ? (
                    <>
                      <span className="font-sans text-sm">{e.clientName}</span>
                      <div className="text-muted-foreground">{e.client}</div>
                    </>
                  ) : (
                    e.client
                  )}
                </TableCell>
                <TableCell className="font-mono text-xs break-all">
                  {e.name}
                  {e.rule && e.rule !== e.name && <div className="text-muted-foreground">règle : {e.rule}</div>}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{e.qtype}</TableCell>
                <TableCell>
                  <Badge variant={RESULT[e.action].variant}>{RESULT[e.action].label}</Badge>
                </TableCell>
                <TableCell className="text-right font-mono text-xs whitespace-nowrap text-muted-foreground tabular-nums">
                  {e.durationUs >= 1000 ? `${Math.round(e.durationUs / 1000)} ms` : `${e.durationUs} µs`}
                </TableCell>
                <TableCell className="text-right">
                  {e.action !== "error" && (
                    <Button variant="ghost" size="sm" onClick={() => act(e)}>
                      {e.action === "blocked" ? "Autoriser" : "Bloquer"}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
