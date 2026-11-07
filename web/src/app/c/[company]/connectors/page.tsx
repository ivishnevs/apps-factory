import { redirect } from "next/navigation";

// nodes-naming: the page was called Connectors; old links go to Nodes.
export default async function Page({ params }: { params: Promise<{ company: string }> }) {
  const { company } = await params;
  redirect(`/c/${encodeURIComponent(company)}/nodes`);
}
