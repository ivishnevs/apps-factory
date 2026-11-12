import type { Message } from "@/lib/api/types";

// What people read of a message (people-topics D7): Core's text when it sent one, else the body without the technical
// lines Core writes for agents (the exact version or call JSON, "Artifact approval <id>:" prefixes, "Escalation <reason>
// on task <id>:") and with full ids shortened. Mirrors internal/storage PeopleText for items Core served without text.
export function cleanBody(body: string): string {
  // A choice approval's options list is for agents: people see the options themselves (artifact-choice-approvals).
  const choice = body.startsWith("Artifact choice approval ");
  return body
    .replace(/^(Exact artifact version|Exact call): .*$/gm, "")
    .replace(choice ? /^(Options:|\d+\. .*\([A-Za-z0-9][A-Za-z0-9_-]*@[^)\s]+\).*)$/gm : /$^/, "")
    .replace(/^(Artifact approval|Artifact choice approval|Approval) [0-9a-f]{32}: /, "")
    .replace(/^Escalation [a-z_]+ on (task|quota key) [^\n:]*?: /, "")
    .replace(/\b[0-9a-f]{32}\b/g, (id) => id.slice(0, 8))
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

export const peopleText = (m: Pick<Message, "body" | "text">) => m.text || cleanBody(m.body ?? "");
