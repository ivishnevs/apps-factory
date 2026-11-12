import type { Action, Approval } from "@/lib/api/types";
import { optionName } from "./choice-approval";

export type DecideKind = "approve" | "reject" | "choose" | "changes";

// approvalDecision is an approval decision as the owner action to send, the confirmation to ask and the toast after:
// approve or reject a yes/no approval; choose an option, ask for changes or reject all on a choice
// (artifact-choice-approvals).
export function approvalDecision(kind: DecideKind, ap: Approval, reason: string, option?: number) {
  const action: Action = { Kind: kind, ApprovalID: ap.id, Version: Math.max(ap.version, 1), Text: reason };
  const what = ap.artifact && !ap.options?.length ? ap.artifact.name || ap.artifact.id : ap.options?.length ? `the choice of ${ap.options.length} versions` : ap.call.name;
  const asked = `${ap.agent} asked${ap.task_id ? ` on ${ap.task_id}` : ""}.`;
  switch (kind) {
    case "choose": {
      action.Option = option;
      const name = optionName(ap, option ?? 0);
      return {
        action,
        ask: { title: `Choose ${name}?`, description: `${asked}\n${name} is approved; the other versions are not chosen and stay usable.${reason ? `\nYour comment: ${reason}` : ""}`, confirm: `Choose ${name}` },
        done: `${name} chosen`,
      };
    }
    case "changes":
      return {
        action,
        ask: { title: "Ask for changes?", description: `${asked}\nNo version is approved. What to change: ${reason}`, confirm: "Ask for changes" },
        done: "Changes requested",
      };
    case "reject":
      return {
        action,
        ask: { title: ap.options?.length ? "Reject all options?" : `Reject ${what}?`, description: `${asked}\nNote: ${reason}`, confirm: ap.options?.length ? "Reject all" : "Reject", destructive: true },
        done: "Rejected",
      };
    default:
      return {
        action,
        ask: { title: `Approve ${what}?`, description: `${asked}\nNote: ${reason}`, confirm: "Approve" },
        done: "Approved",
      };
  }
}
