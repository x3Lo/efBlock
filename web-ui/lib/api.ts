// Client de l'API d'efBlock : tous les appels passent par ici.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

// Déclenché quand le serveur répond 401 : AuthGate affiche alors la page de connexion.
export const UNAUTHORIZED_EVENT = "efblock:unauthorized";

export async function api<T>(
  path: string,
  opts: { method?: string; body?: unknown } = {},
): Promise<T> {
  const res = await fetch(path, {
    method: opts.method ?? "GET",
    headers: {
      "Content-Type": "application/json",
      // En-tête exigé par le serveur pour toute modification (protection CSRF).
      "X-Requested-With": "efblock",
    },
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    credentials: "same-origin",
    cache: "no-store",
  });
  if (res.status === 401 && path !== "/api/login" && path !== "/api/session") {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  }
  if (!res.ok) {
    let msg = `Erreur ${res.status}`;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* corps vide ou non JSON */
    }
    throw new ApiError(res.status, msg);
  }
  return res.json() as Promise<T>;
}

// --- Types des réponses ---

export type Count = { Name: string; Count: number };
export type Bucket = { start: string; total: number; blocked: number };
export type Blocking = { mode: "on" | "off" | "paused"; pausedUntil?: string };

export type Stats = {
  logEnabled: boolean;
  hours: number;
  summary: {
    total: number;
    blocked: number;
    cached: number;
    forwarded: number;
    errors: number;
    blockedPct: number;
  };
  topBlocked: Count[];
  topDomains: Count[];
  topClients: Count[];
  timeline: Bucket[];
  blocking: Blocking;
  filter: { Blocked: number; Exceptions: number };
  cache: { Hits: number; Misses: number; Entries: number } | null;
};

export type QueryEntry = {
  time: string;
  client: string;
  name: string;
  qtype: string;
  action: "blocked" | "cached" | "forwarded" | "error";
  rule: string;
  rcode: string;
  durationUs: number;
};

export type DomainEntry = { domain: string; source: "config" | "ui" };

export type ListInfo = {
  name: string;
  url: string;
  source: "config" | "ui";
  blocked: number;
  exceptions: number;
  updatedAt?: string;
  error?: string;
};
export type ListsResponse = {
  lists: ListInfo[];
  totalBlocked: number;
  totalExceptions: number;
};

export const nf = new Intl.NumberFormat("fr-FR");
export const fmtTime = (iso: string) =>
  new Date(iso).toLocaleTimeString("fr-FR", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
export const fmtDateTime = (iso: string) =>
  new Date(iso).toLocaleString("fr-FR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
