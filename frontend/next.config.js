const apiBaseUrl = (
  process.env.NEXT_PUBLIC_API_URL || "http://127.0.0.1:18081"
).trim().replace(/\/+$/, "");

/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  outputFileTracingRoot: __dirname,
  distDir: process.env.NEXT_DIST_DIR || '.next',
  reactStrictMode: true,
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${apiBaseUrl}/api/:path*`,
      },
    ];
  },
  webpack: (config) => {
    config.resolve.fallback = {
      ...config.resolve.fallback,
      fs: false,
      path: false,
    };
    return config;
  },
};

module.exports = nextConfig;
