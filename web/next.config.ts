import type { NextConfig } from "next";

// The owner API (`factory web`, Go) holds the owner token; the browser only talks to this app, which
// proxies /api/* to it. FACTORY_WEB_API overrides the default address.
const api = process.env.FACTORY_WEB_API ?? "http://127.0.0.1:8090";

const nextConfig: NextConfig = {
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${api}/api/:path*` }];
  },
};

export default nextConfig;
