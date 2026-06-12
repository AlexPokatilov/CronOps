export function PhaseBadge({ phase }: { phase?: string }) {
  const cls =
    phase === "Active"
      ? "active"
      : phase === "Suspended"
        ? "suspended"
        : phase === "Invalid"
          ? "invalid"
          : "neutral";
  return <span className={`badge ${cls}`}>{phase ?? "Pending"}</span>;
}

export function ResultBadge({ result }: { result?: string }) {
  if (!result) return <span className="badge neutral">—</span>;
  return (
    <span className={`badge ${result === "Success" ? "success" : "failed"}`}>
      {result}
    </span>
  );
}

export function formatTime(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString();
}
