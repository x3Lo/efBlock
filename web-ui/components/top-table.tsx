import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
import { nf, Rank } from "@/lib/api";

// Classement : un nom, un nombre, et éventuellement une action par ligne.
export default function TopTable({
  title,
  rows,
  empty = "Rien sur cette période.",
  action,
}: {
  title: string;
  rows: Rank[];
  empty?: string;
  action?: (name: string) => React.ReactNode;
}) {
  return (
    <section>
      <h2 className="mb-1 text-sm font-semibold">{title}</h2>
      {rows.length === 0 ? (
        <p className="py-3 text-muted-foreground">{empty}</p>
      ) : (
        <Table>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={r.name}>
                <TableCell className="font-mono text-xs break-all">
                  {r.label ? (
                    <>
                      <span className="font-sans text-sm">{r.label}</span>
                      <span className="ml-2 text-muted-foreground">{r.name}</span>
                    </>
                  ) : (
                    r.name
                  )}
                </TableCell>
                <TableCell className="w-16 text-right font-mono text-xs tabular-nums">{nf.format(r.count)}</TableCell>
                {action && <TableCell className="w-24 text-right">{action(r.name)}</TableCell>}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}
