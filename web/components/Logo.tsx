// Treasure Dash mark: a gold coin with three rising bars – money in, tracked.
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 40 40" className={className} role="img" aria-label="Treasure Dash">
      <rect width="40" height="40" rx="10" fill="#1d3461" />
      <circle cx="20" cy="20" r="13" fill="#d4a63c" />
      <circle cx="20" cy="20" r="10.5" fill="none" stroke="#f3d68a" strokeWidth="1.2" />
      <rect x="13.2" y="21" width="3.4" height="6" rx="1" fill="#14284a" />
      <rect x="18.3" y="17" width="3.4" height="10" rx="1" fill="#14284a" />
      <rect x="23.4" y="13" width="3.4" height="14" rx="1" fill="#14284a" />
    </svg>
  );
}
