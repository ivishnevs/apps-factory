import { CheckCircle2, CircleDot } from "lucide-react";
import type { ApprovalComment } from "@/lib/api/types";
import { cn } from "@/lib/utils";

// ApprovalCommentOutcome: what became of the comment the owner gave with an approval decision
// (approval-comments-handled), e.g. "done by launch-creative: frame 1 without the window" or "not handled yet".
export function ApprovalCommentOutcome({ comment, className }: { comment?: ApprovalComment; className?: string }) {
  if (!comment) return null;
  const tone = comment.status === "open" ? "text-amber-700 dark:text-amber-400"
    : comment.status === "declined" ? "text-red-700 dark:text-red-400" : "text-muted-foreground";
  const Icon = comment.status === "done" ? CheckCircle2 : CircleDot;
  return (
    <span className={cn("text-xs wrap-anywhere", tone, className)} aria-label="What became of your comment">
      <Icon className="mr-1 inline size-3 align-[-2px]" />
      {comment.outcome || "not handled yet"}
    </span>
  );
}
