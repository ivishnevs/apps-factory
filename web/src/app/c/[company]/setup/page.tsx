"use client";

import { useEffect } from "react";
import { useRouter } from "@/components/app/link";

// A new company's setup (user-accounts D1): the team page until the team-builder wizard takes this place.
export default function Page() {
  const router = useRouter();
  useEffect(() => { router.replace("/teams?welcome=1"); }, [router]);
  return null;
}
