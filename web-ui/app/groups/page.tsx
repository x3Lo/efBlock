"use client";

import { useEffect, useState } from "react";

import ConfirmButton from "@/components/confirm-button";
import Field from "@/components/field";
import ListField from "@/components/list-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, Group, GroupsResponse } from "@/lib/api";

const emptyGroup = (): Group => ({ name: "", clients: [], mode: "custom", all_lists: true, lists: [], allow: [], block: [] });

export default function Groups() {
  const [data, setData] = useState<GroupsResponse | null>(null);
  const [draft, setDraft] = useState<Group[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ error: boolean; text: string } | null>(null);

  useEffect(() => {
    api<GroupsResponse>("/api/groups")
      .then((r) => {
        setData(r);
        setDraft(r.groups);
      })
      .catch((e) => setMsg({ error: true, text: e instanceof Error ? e.message : "Erreur" }));
  }, []);

  if (!data) return <p className="text-muted-foreground">{msg?.text || "Chargement…"}</p>;

  const dirty = JSON.stringify(draft) !== JSON.stringify(data.groups);
  const update = (i: number, patch: Partial<Group>) => setDraft((d) => d.map((g, j) => (j === i ? { ...g, ...patch } : g)));
  const move = (i: number, delta: number) =>
    setDraft((d) => {
      const j = i + delta;
      if (j < 0 || j >= d.length) return d;
      const next = [...d];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });

  async function save() {
    setBusy(true);
    setMsg(null);
    try {
      const r = await api<GroupsResponse>("/api/groups", { method: "PUT", body: { groups: draft } });
      setData(r);
      setDraft(r.groups);
      setMsg({ error: false, text: "Enregistré et appliqué." });
    } catch (e) {
      setMsg({ error: true, text: e instanceof Error ? e.message : "Erreur" });
    } finally {
      setBusy(false);
    }
  }

  // Les appareils déjà rangés dans un groupe ne sont plus proposés.
  const taken = new Set(draft.flatMap((g) => g.clients));
  const suggestions = data.clients.filter((c) => !taken.has(c.ip));

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Groupes de clients</h1>
        <p className="mt-1 max-w-2xl text-muted-foreground">
          Un groupe applique ses propres règles aux appareils qu&apos;il contient : aucun blocage, ou seulement certaines listes, avec des
          domaines autorisés ou bloqués en plus. Les appareils qui ne sont dans aucun groupe gardent les réglages par défaut.
        </p>
        <p className="mt-2 max-w-2xl text-xs text-muted-foreground">
          Un appareil est reconnu par son <strong>adresse IP</strong>. Réserve-lui une adresse fixe dans le DHCP de ta box ou de pfSense, sinon
          il peut en changer et sortir de son groupe. Si un appareil figure dans plusieurs groupes, c&apos;est le <strong>premier</strong> de la
          liste qui s&apos;applique : l&apos;ordre compte.
        </p>
      </div>

      {draft.length === 0 && <p className="text-muted-foreground">Aucun groupe pour l&apos;instant.</p>}

      {draft.map((g, i) => (
        <section key={i} className="max-w-2xl border-t pt-5">
          <div className="mb-4 flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-semibold">{g.name.trim() || "Nouveau groupe"}</h2>
            <Badge>{g.mode === "off" ? "aucun blocage" : "filtrage personnalisé"}</Badge>
            <span className="flex-1" />
            <Button variant="ghost" size="sm" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Monter ce groupe">
              Monter
            </Button>
            <Button variant="ghost" size="sm" disabled={i === draft.length - 1} onClick={() => move(i, 1)} aria-label="Descendre ce groupe">
              Descendre
            </Button>
            <ConfirmButton ariaLabel={`Supprimer le groupe ${g.name}`} label="Supprimer" onConfirm={() => setDraft((d) => d.filter((_, j) => j !== i))} />
          </div>

          <div className="flex flex-col gap-4">
            <Field label="Nom du groupe">
              <Input className="w-64" value={g.name} maxLength={40} placeholder="Invités" onChange={(e) => update(i, { name: e.target.value })} />
            </Field>

            <Field label="Appareils, un par ligne" help="Une adresse (192.168.1.50) ou un réseau entier (192.168.1.0/24).">
              <ListField label={`Appareils du groupe ${g.name}`} rows={3} value={g.clients} onChange={(v) => update(i, { clients: v })} placeholder="192.168.1.50" />
            </Field>
            {suggestions.length > 0 && (
              <Field label="Ajouter un appareil vu récemment">
                <Select
                  className="w-full max-w-sm"
                  value=""
                  onChange={(e) => {
                    if (e.target.value) update(i, { clients: [...g.clients, e.target.value] });
                  }}
                >
                  <option value="">Choisir…</option>
                  {suggestions.map((c) => (
                    <option key={c.ip} value={c.ip}>
                      {c.label ? `${c.label} (${c.ip})` : c.ip}
                    </option>
                  ))}
                </Select>
              </Field>
            )}

            <Field label="Filtrage">
              <Select className="w-72" value={g.mode} onChange={(e) => update(i, { mode: e.target.value as Group["mode"] })}>
                <option value="custom">Personnalisé : mes listes et règles</option>
                <option value="off">Aucun blocage pour ces appareils</option>
              </Select>
            </Field>

            {g.mode === "custom" && (
              <>
                <fieldset className="flex flex-col gap-2">
                  <legend className="mb-1 text-xs font-medium">Listes de filtres utilisées</legend>
                  <label className="flex items-center gap-2">
                    <input type="radio" className="accent-primary" name={`lists-${i}`} checked={g.all_lists} onChange={() => update(i, { all_lists: true })} />
                    <span>Toutes les listes</span>
                  </label>
                  <label className="flex items-center gap-2">
                    <input type="radio" className="accent-primary" name={`lists-${i}`} checked={!g.all_lists} onChange={() => update(i, { all_lists: false })} />
                    <span>Seulement certaines</span>
                  </label>
                  {!g.all_lists && (
                    <div className="ml-6 flex flex-col gap-1.5">
                      {data.lists.map((l) => (
                        <label key={l.url} className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            className="size-4 accent-primary"
                            checked={g.lists.includes(l.url)}
                            onChange={(e) => update(i, { lists: e.target.checked ? [...g.lists, l.url] : g.lists.filter((u) => u !== l.url) })}
                          />
                          <span>{l.name}</span>
                        </label>
                      ))}
                      {g.lists.length === 0 && <span className="text-xs text-muted-foreground">Aucune liste cochée : seules les règles ci-dessous s&apos;appliquent.</span>}
                    </div>
                  )}
                </fieldset>

                <Field label="Toujours autorisés pour ce groupe" help="Prioritaire sur tout le reste, listes comprises.">
                  <ListField label={`Autorisés pour ${g.name}`} rows={2} value={g.allow} onChange={(v) => update(i, { allow: v })} placeholder="ecole.example.org" />
                </Field>
                <Field label="Toujours bloqués pour ce groupe" help="En plus des listes. Les sous-domaines sont inclus.">
                  <ListField label={`Bloqués pour ${g.name}`} rows={2} value={g.block} onChange={(v) => update(i, { block: v })} placeholder="jeux.example.net" />
                </Field>
              </>
            )}
          </div>
        </section>
      ))}

      <div className="flex max-w-2xl flex-wrap items-center gap-2 border-t pt-5">
        <Button variant="outline" size="sm" disabled={draft.length >= 30} onClick={() => setDraft((d) => [...d, emptyGroup()])}>
          Ajouter un groupe
        </Button>
        <Button size="sm" disabled={!dirty || busy} onClick={save}>
          Enregistrer
        </Button>
        {dirty && !busy && <span className="text-xs text-muted-foreground">Modifications non enregistrées</span>}
        {msg && (
          <span role={msg.error ? "alert" : "status"} className={`text-sm ${msg.error ? "text-destructive" : "text-muted-foreground"}`}>
            {msg.text}
          </span>
        )}
      </div>
    </div>
  );
}
