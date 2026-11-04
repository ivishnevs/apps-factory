"use client";

// The last resort when even the root layout fails: plain HTML, no providers.
export default function GlobalError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <html lang="en">
      <body style={{ fontFamily: "system-ui, sans-serif", display: "flex", minHeight: "100dvh", alignItems: "center", justifyContent: "center", textAlign: "center", padding: 16 }}>
        <div>
          <h1 style={{ fontSize: 20, fontWeight: 600 }}>App Factory could not load</h1>
          <p style={{ color: "#666", fontSize: 14 }}>Try again in a moment.</p>
          <button onClick={reset} style={{ marginTop: 12, padding: "8px 16px", borderRadius: 8, border: "1px solid #ccc", cursor: "pointer" }}>Try again</button>
        </div>
      </body>
    </html>
  );
}
