import { Bucket, nf } from "../lib/api";

// Histogramme : hauteur = requêtes totales, partie rouge = bloquées.
export default function Timeline({ data, hours }: { data: Bucket[]; hours: number }) {
  const max = Math.max(1, ...data.map((b) => b.total));
  const fmt = (iso: string) =>
    new Date(iso).toLocaleString("fr-FR", hours > 24
      ? { weekday: "short", hour: "2-digit", minute: "2-digit" }
      : { hour: "2-digit", minute: "2-digit" });

  if (data.length === 0) return <p className="muted">Pas encore de données.</p>;
  return (
    <figure style={{ margin: 0 }}>
      <div className="chart" role="img" aria-label="Requêtes au fil du temps">
        {data.map((b) => (
          <div className="col" key={b.start} title={`${fmt(b.start)} : ${nf.format(b.total)} requêtes, ${nf.format(b.blocked)} bloquées`}>
            <div className="total" style={{ height: `${(b.total / max) * 100}%` }}>
              <div className="blk" style={{ height: b.total ? `${(b.blocked / b.total) * 100}%` : 0 }} />
            </div>
          </div>
        ))}
      </div>
      <div className="axis">
        <span>{fmt(data[0].start)}</span>
        <span>{fmt(data[data.length - 1].start)}</span>
      </div>
      <div className="legend">
        <span><i style={{ background: "var(--bar)" }} />Requêtes</span>
        <span><i style={{ background: "var(--blocked)" }} />Bloquées</span>
      </div>
    </figure>
  );
}
