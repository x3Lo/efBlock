"use client";

import { Download, Upload } from "lucide-react";
import { useRef, useState } from "react";

import ConfirmButton from "@/components/confirm-button";
import { Button } from "@/components/ui/button";
import { api, fmtDateTime, nf } from "@/lib/api";

type BackupFile = {
  format?: string;
  created?: string;
  efblock_version?: string;
  state?: { allowlist?: string[]; blocklist?: string[]; lists?: unknown[]; groups?: unknown[] };
};

// Sauvegarde (un fichier .json téléchargé) et restauration de la configuration faite depuis l'interface.
export default function BackupSection() {
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<{ name: string; data: BackupFile } | null>(null);
  const [msg, setMsg] = useState<{ error: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function pick(e: React.ChangeEvent<HTMLInputElement>) {
    setMsg(null);
    const f = e.target.files?.[0];
    e.target.value = ""; // permet de rechoisir le même fichier
    if (!f) return;
    try {
      const data = JSON.parse(await f.text()) as BackupFile;
      if (data.format !== "efblock-sauvegarde") throw new Error();
      setFile({ name: f.name, data });
    } catch {
      setFile(null);
      setMsg({ error: true, text: "Ce fichier n'est pas une sauvegarde d'efBlock." });
    }
  }

  async function restore() {
    if (!file) return;
    setBusy(true);
    setMsg(null);
    try {
      await api("/api/restore", { method: "POST", body: file.data });
      setMsg({ error: false, text: "Configuration restaurée. Rechargement de la page…" });
      setFile(null);
      setTimeout(() => location.reload(), 1500);
    } catch (e) {
      setMsg({ error: true, text: e instanceof Error ? e.message : "Erreur" });
    } finally {
      setBusy(false);
    }
  }

  const st = file?.data.state;
  return (
    <section className="max-w-3xl rounded-xl border bg-card p-5">
      <h2 className="mb-1 text-sm font-semibold">Sauvegarde</h2>
      <p className="mb-4 text-muted-foreground">
        Un fichier avec tes réglages, tes listes de filtres, tes domaines autorisés et bloqués, et tes groupes. Utile avant une réinstallation.
        Le mot de passe et les adresses d&apos;écoute ne sont pas sauvegardés : ils restent ceux de la machine.
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Button asChild variant="outline" size="sm">
          <a href="/api/backup" download>
            <Download className="size-3.5" aria-hidden /> Télécharger une sauvegarde
          </a>
        </Button>
        <Button variant="outline" size="sm" onClick={() => input.current?.click()} disabled={busy}>
          <Upload className="size-3.5" aria-hidden /> Restaurer depuis un fichier
        </Button>
        <input ref={input} type="file" accept=".json,application/json" className="hidden" onChange={pick} aria-label="Fichier de sauvegarde" />
      </div>

      {file && (
        <div className="mt-4 rounded-lg border bg-background/40 p-3">
          <p className="font-medium">{file.name}</p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {file.data.created ? `Créée le ${fmtDateTime(file.data.created)}` : "Date inconnue"}
            {file.data.efblock_version ? `, efBlock ${file.data.efblock_version}` : ""} : {nf.format(st?.lists?.length ?? 0)} liste(s),{" "}
            {nf.format(st?.groups?.length ?? 0)} groupe(s), {nf.format(st?.blocklist?.length ?? 0)} domaine(s) bloqué(s),{" "}
            {nf.format(st?.allowlist?.length ?? 0)} autorisé(s).
          </p>
          <p className="mt-2 text-xs text-muted-foreground">La restauration remplace ta configuration actuelle par celle du fichier.</p>
          <div className="mt-3 flex gap-2">
            <ConfirmButton variant="default" label="Restaurer cette sauvegarde" onConfirm={restore} />
            <Button variant="ghost" size="sm" onClick={() => setFile(null)} disabled={busy}>
              Annuler
            </Button>
          </div>
        </div>
      )}

      {msg && (
        <p role={msg.error ? "alert" : "status"} className={`mt-3 text-sm ${msg.error ? "text-destructive" : "text-muted-foreground"}`}>
          {msg.text}
        </p>
      )}
    </section>
  );
}
