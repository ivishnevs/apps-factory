import Link from "next/link";

export const metadata = { title: "Security · App Factory" };

// Security (connector-trust 3.1): what a node can do on your machine and what you control there, in plain words.
const sections: { title: string; items: string[] }[] = [
  { title: "What agents may touch", items: [
    "New nodes confine agents to their task's git worktree: they write only there (and in its repository), and cannot read your SSH, cloud and GPG keys or the node's own keys.",
    "You can give a single project full access (policy.full_access in node.yaml), or the whole node (sandbox: full).",
  ] },
  { title: "What you control on the machine", items: [
    "factory-node pause stops new work at once; App Factory cannot lift it. factory-node resume starts it again.",
    "policy.projects and policy.runtimes in node.yaml limit which projects and runtimes run on this machine.",
    "factory-node activity shows what ran here; factory-node doctor -security shows the settings above.",
    "factory-node login claude|codex gives the node its own logins; a changed account stops that runtime until you accept it.",
    "factory-node uninstall removes the node from the machine.",
  ] },
  { title: "What the node never does", items: [
    "It never sends your credentials to App Factory: runtimes get only an allowlist of environment variables; company secrets reach a run sealed to that node.",
    "Task branches are scanned for secrets before they are pushed.",
    "Every release ships with checksums and a software bill of materials (SBOM.json).",
  ] },
];

export default function Page() {
  return (
    <main className="mx-auto max-w-3xl px-4 py-12 sm:px-6">
      <Link href="/" className="text-sm text-muted-foreground hover:text-foreground">App Factory</Link>
      <h1 className="mt-4 text-3xl font-semibold tracking-tight">Security</h1>
      <p className="mt-3 text-muted-foreground">A node is a program on your machine that runs your company&apos;s agents with your Claude and Codex subscriptions. This is what it can do there and what you control.</p>
      {sections.map((s) => (
        <section key={s.title} className="mt-8">
          <h2 className="text-lg font-medium">{s.title}</h2>
          <ul className="mt-3 list-disc space-y-2 pl-5 text-[15px] leading-relaxed">{s.items.map((i) => <li key={i}>{i}</li>)}</ul>
        </section>
      ))}
    </main>
  );
}
