"use client";

import { useQuery } from "@tanstack/react-query";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api/client";
import type { Agent } from "@/lib/api/types";

// Review coverage: every task outside the backlog names its reviewer — an agent, you (the owner) or a member.
// The review policy's suggestion prefills the field (GET /tasks/review-suggestion).

export const reviewerName = (by?: string) => (!by ? "" : by === "owner" ? "You" : by.startsWith("member:") ? by.slice(7) : by);

export function useReviewSuggestion(assignee: string) {
  return useQuery({
    queryKey: ["review-suggestion", assignee],
    queryFn: () => api<{ review_by: string }>(`/tasks/review-suggestion?assignee=${encodeURIComponent(assignee)}`),
    enabled: !!assignee,
    staleTime: 60_000,
  });
}

export function ReviewerSelect({ id, value, onChange, agents, assignee }: {
  id: string; value: string; onChange: (v: string) => void; agents: Agent[]; assignee: string;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id={id} className="w-full data-[size=default]:h-11 md:data-[size=default]:h-9"><SelectValue placeholder="Choose a reviewer" /></SelectTrigger>
      <SelectContent position="popper">
        <SelectItem value="owner">You</SelectItem>
        {agents.filter((a) => a.name !== assignee).map((a) => (
          <SelectItem key={a.name} value={a.name}>{a.name}{a.role ? <span className="text-muted-foreground"> · {a.role}</span> : null}</SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
