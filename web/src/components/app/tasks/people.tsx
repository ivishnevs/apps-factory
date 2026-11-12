"use client";

import { useQuery } from "@tanstack/react-query";
import { core, useCompanyInfo, useMe } from "@/lib/api/client";
import type { Role } from "@/lib/api/types-account";
import type { Task } from "@/lib/api/types";

// People as assignees (people-assignees): a task's assignee is an agent or a person of the company, "member:<user>".
// Core runs nothing for a person's task; the person (or an owner) moves it: start, submit for review with the pull
// request, done once the approved pull request is merged.

export interface Person { user: string; email: string; name: string; role: Role }

export const PERSON = "member:";
export const personUser = (assignee?: string) => (assignee?.startsWith(PERSON) ? assignee.slice(PERSON.length) : "");
export const isPerson = (assignee?: string) => !!personUser(assignee);

// useMembers: the company's people (GET /memberships), for pickers, names and avatars.
export function useMembers() {
  return useQuery({ queryKey: ["members"], queryFn: () => core<Person[]>("/memberships"), staleTime: 60_000, retry: false });
}

export function displayName(p?: Person) {
  if (!p) return "";
  return p.name || p.email.split("@")[0] || p.user;
}

// assigneeName: an agent's name as is, a person's name (else their user id), "" when unassigned.
export function assigneeName(assignee: string | undefined, people?: Person[]) {
  const user = personUser(assignee);
  if (!user) return assignee ?? "";
  return displayName(people?.find((p) => p.user === user)) || user;
}

// useMine: the signed-in person as an assignee ("member:<user>"), "" for a token or the operator.
export function useMine() {
  const me = useMe().data;
  return me?.kind === "session" && me.user ? PERSON + me.user.id : "";
}

// useMovesPersonTask: whether the viewer may move a person's task: the person, or an owner (a token or the
// operator acts as the owner). Core checks it again.
export function useMovesPersonTask(task: Pick<Task, "assignee">) {
  const me = useMe().data;
  const role = useCompanyInfo().data?.role;
  if (!isPerson(task.assignee)) return false;
  if (!me || me.kind !== "session") return true;
  return role === "owner" || task.assignee === PERSON + (me.user?.id ?? "");
}

// personStep: the next move of a person's task, or null.
export type PersonStep = "work_start" | "work_submit" | "work_done";
export function personSteps(t: Task): PersonStep[] {
  if (!isPerson(t.assignee)) return [];
  const approved = t.status === "READY" && t.implementer === t.assignee && !!t.submission && !t.review_feedback;
  if (approved) return ["work_done"];
  if (t.status === "IN_PROGRESS") return ["work_submit"];
  if (t.status === "REWORK" || t.status === "READY" && !!t.review_feedback) return ["work_start", "work_submit"];
  if (t.status === "READY") return ["work_start", "work_submit"];
  return [];
}
