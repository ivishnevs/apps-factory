"use client";

import { useEffect } from "react";
import { useRouter } from "@/components/app/link";

// The single Team page became Teams (company-teams 7.1).
export default function Page() {
  const router = useRouter();
  useEffect(() => { router.replace("/teams" + window.location.search); }, [router]);
  return null;
}
