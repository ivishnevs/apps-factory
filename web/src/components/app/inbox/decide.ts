import type { Action, Approval } from "@/lib/api/types";
import { isChoice, isSingle, optionName } from "./choice-approval";

export type DecideKind = "approve" | "reject" | "choose" | "changes";

// approvalDecision is an approval decision as the owner action to send, the confirmation to ask and the toast after.
// Every artifact approval (artifact-choice-approvals, one approval model) is chosen (approving a single version is
// choosing its only option), sent back for changes or rejected; a tool call's approval is approved or rejected.
export function approvalDecision(kind: DecideKind, ap: Approval, reason: string, option?: number) {
  const action: Action = { Kind: kind, ApprovalID: ap.id, Version: Math.max(ap.version, 1), Text: reason };
  const single = isSingle(ap);
  const what = single ? optionName(ap, 1) : isChoice(ap) ? `the choice of ${ap.options!.length} versions` : ap.call.name;
  const asked = `${ap.agent} asked${ap.task_id ? ` on ${ap.task_id}` : ""}.`;
  const comment = reason ? `\nYour comment: ${reason}` : "";
  switch (kind) {
    case "choose": {
      action.Option = option;
      if (single) {
        return {
          action,
          ask: { title: `Approve ${what}?`, description: `${asked}${comment}`, confirm: "Approve" },
          done: "Approved",
        };
      }
      const name = optionName(ap, option ?? 0);
      return {
        action,
        ask: { title: `Choose ${name}?`, description: `${asked}\n${name} is approved; the other versions are not chosen and stay usable.${comment}`, confirm: `Choose ${name}` },
        done: `${name} chosen`,
      };
    }
    case "changes":
      return {
        action,
        ask: { title: "Ask for changes?", description: `${asked}\n${single ? "It is not approved." : "No version is approved."} What to change: ${reason}`, confirm: "Ask for changes" },
        done: "Changes requested",
      };
    case "reject":
      return {
        action,
        ask: { title: isChoice(ap) && !single ? "Reject all options?" : `Reject ${what}?`, description: `${asked}\nNote: ${reason}`, confirm: isChoice(ap) && !single ? "Reject all" : "Reject", destructive: true },
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
