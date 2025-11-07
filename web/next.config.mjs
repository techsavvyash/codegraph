/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,

  // Environment variables exposed to the browser
  env: {
    NEXT_PUBLIC_GRAPHQL_URL: process.env.NEXT_PUBLIC_GRAPHQL_URL || 'http://localhost:8080/graphql',
  },

  // Disable x-powered-by header
  poweredByHeader: false,

  // Image optimization configuration
  images: {
    domains: [],
  },

  // Experimental features
  experimental: {
    // Enable future optimizations
  },

  // Webpack configuration for Cytoscape
  webpack: (config, { isServer }) => {
    // Cytoscape can have issues with SSR, ensure it's only loaded on client
    if (!isServer) {
      config.resolve.fallback = {
        ...config.resolve.fallback,
        fs: false,
        net: false,
        tls: false,
      };
    }

    return config;
  },
};

export default nextConfig;
