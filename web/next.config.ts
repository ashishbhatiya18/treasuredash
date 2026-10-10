import type { NextConfig } from "next";

const isDev = process.env.NODE_ENV === "development";

// Production: a static export that the Go binary embeds and serves.
// Development: `next dev` proxies /api to the Go server on :8080.
const config: NextConfig = isDev
  ? {
      async rewrites() {
        return [{ source: "/api/:path*", destination: "http://localhost:8080/api/:path*" }];
      },
    }
  : { output: "export", images: { unoptimized: true } };

export default config;
