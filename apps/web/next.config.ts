import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  transpilePackages: ["@multica/ui", "@multica/store"],
};

export default nextConfig;
