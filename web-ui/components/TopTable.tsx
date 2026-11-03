import { Count, nf } from "../lib/api";

// Classement avec une barre derrière chaque ligne (proportionnelle au maximum).
export default function TopTable({
  title,
  rows,
  empty = "Rien pour l'instant.",
  action,
}: {
  title: string;
  rows: Count[];
  empty?: string;
  action?: (name: string) => React.ReactNode;
}) {
  const max = Math.max(1, ...rows.map((r) => r.Count));
  return (
    <section className="card">
      <h2>{title}</h2>
      {rows.length === 0 ? (
        <p className="muted">{empty}</p>
      ) : (
        <div className="table-wrap">
          <table>
            <tbody>
              {rows.map((r) => (
                <tr key={r.Name}>
                  <td className="name share">
                    <div className="fill" style={{ width: `${(r.Count / max) * 100}%` }} />
                    <span className="mono">{r.Name}</span>
                  </td>
                  <td className="num">{nf.format(r.Count)}</td>
                  {action && <td>{action(r.Name)}</td>}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
