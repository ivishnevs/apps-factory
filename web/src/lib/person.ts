"use client";

import { useMe } from "@/lib/api/client";

// usePerson (people-attribution): a person's label — "you" for the signed-in person or a record without a person
// (the CLI, older rows), else their name.
export function usePerson() {
  const me = useMe().data?.user;
  return (person?: string, personId?: string, you = "you") => {
    if (!person && !personId) return you;
    if (personId && me?.id === personId) return you;
    if (person && (person === me?.name || (me?.email && person === me.email.split("@")[0]))) return you;
    return person || personId || you;
  };
}
