import { Shell } from "@/components/app/shell";

// The signed-in app. The SaaS adds (marketing) and (auth) route groups beside it.
export default function AppLayout({ children }: { children: React.ReactNode }) {
  return <Shell>{children}</Shell>;
}
