export default function Field({ label, help, children }: { label: string; help?: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium">{label}</span>
      {children}
      {help && <span className="text-xs text-muted-foreground">{help}</span>}
    </label>
  );
}
