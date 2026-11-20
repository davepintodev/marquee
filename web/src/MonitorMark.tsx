export function MonitorMark({ have, on, name, busy, onToggle }: { have: boolean; on: boolean; name: string; busy: boolean; onToggle: () => void }) {
  const label = on ? "Monitored. Stop monitoring" : "Not monitored. Monitor";
  return (
    <button
      type="button"
      className="mk"
      data-have={have}
      data-on={on}
      aria-label={`${label} ${name}`}
      title={on ? (have ? "Have it, monitored. Click to stop monitoring" : "Monitored. Click to stop monitoring") : "Not monitored. Click to monitor"}
      aria-disabled={busy}
      onClick={() => !busy && onToggle()}
    >
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <circle className="mk-disc" cx="12" cy="12" r="10" />
        <path className="mk-a" d="M7.5 12.5 L10.5 15.5" />
        <path className="mk-b" d="M10.5 15.5 L16.5 9" />
      </svg>
    </button>
  );
}
