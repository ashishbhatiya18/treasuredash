export function FlatsByFloor({ codes }: { codes: string[] }) {
  const floors = new Map<string, string[]>();
  for (const c of codes) {
    const f = c[0];
    floors.set(f, [...(floors.get(f) ?? []), c]);
  }
  return (
    <div className="space-y-1">
      {[...floors.entries()].map(([floor, cs]) => (
        <div key={floor} className="flex items-baseline gap-2 text-sm">
          <span className="w-14 shrink-0 text-xs text-neutral-500">{floor === "G" ? "Ground" : `Floor ${floor}`}</span>
          <span className="font-medium">{cs.join(", ")}</span>
        </div>
      ))}
    </div>
  );
}
