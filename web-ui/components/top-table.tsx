import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
import { Count, nf } from "@/lib/api";

// Classement : un nom, un nombre, et éventuellement une action par ligne.
export default function TopTable({
  title,
  rows,
  empty = "Rien sur cette période.",
  action,
}: {
  title: string;
  rows: Count[];
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
              <TableRow key={r.Name}>
                <TableCell className="font-mono text-xs break-all">{r.Name}</TableCell>
                <TableCell className="w-16 text-right font-mono text-xs tabular-nums">{nf.format(r.Count)}</TableCell>
                {action && <TableCell className="w-24 text-right">{action(r.Name)}</TableCell>}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}
