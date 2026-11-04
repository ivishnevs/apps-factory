import type { NextConfig } from "next";

// Core serves the web UI's API itself (saas-web D1): the browser only talks to this app, which sends /api/* and the
// sign-in routes /auth/* to Core. FACTORY_WEB_API overrides Core's address (a demo `factory web --demo` too).
const api = process.env.FACTORY_WEB_API ?? process.env.FACTORY_URL ?? "http://127.0.0.1:8080";

const nextConfig: NextConfig = {
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${api}/api/:path*` },
      { source: "/auth/:path*", destination: `${api}/auth/:path*` },
    ];
  },
};

export default nextConfig;
