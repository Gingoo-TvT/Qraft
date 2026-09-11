import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./src/desktop/**/*.{js,ts,jsx,tsx}",
    "./src/pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/components/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        forge: {
          50: "rgb(var(--forge-50, 237 253 246) / <alpha-value>)",
          100: "rgb(var(--forge-100, 211 248 231) / <alpha-value>)",
          200: "rgb(var(--forge-200, 170 240 209) / <alpha-value>)",
          300: "rgb(var(--forge-300, 117 223 181) / <alpha-value>)",
          400: "rgb(var(--forge-400, 58 199 153) / <alpha-value>)",
          500: "rgb(var(--forge-500, 24 172 127) / <alpha-value>)",
          600: "rgb(var(--forge-600, 13 138 102) / <alpha-value>)",
          700: "rgb(var(--forge-700, 11 108 81) / <alpha-value>)",
          800: "rgb(var(--forge-800, 12 85 66) / <alpha-value>)",
          900: "rgb(var(--forge-900, 11 69 55) / <alpha-value>)",
          950: "rgb(var(--forge-950, 5 42 34) / <alpha-value>)",
        },
        anvil: {
          50: "rgb(var(--anvil-50, 246 248 250) / <alpha-value>)",
          100: "rgb(var(--anvil-100, 237 240 244) / <alpha-value>)",
          200: "rgb(var(--anvil-200, 223 229 236) / <alpha-value>)",
          300: "rgb(var(--anvil-300, 193 204 215) / <alpha-value>)",
          400: "rgb(var(--anvil-400, 131 146 165) / <alpha-value>)",
          500: "rgb(var(--anvil-500, 98 114 134) / <alpha-value>)",
          600: "rgb(var(--anvil-600, 71 87 107) / <alpha-value>)",
          700: "rgb(var(--anvil-700, 43 57 75) / <alpha-value>)",
          800: "rgb(var(--anvil-800, 29 42 58) / <alpha-value>)",
          900: "rgb(var(--anvil-900, 21 33 49) / <alpha-value>)",
          950: "rgb(var(--anvil-950, 11 17 27) / <alpha-value>)",
        },
        ember: {
          50: "#fff7ed",
          100: "#ffedd5",
          200: "#fed7aa",
          300: "#fdba74",
          400: "#fb923c",
          500: "#f97316",
          600: "#ea580c",
          700: "#c2410c",
          800: "#9a3412",
          900: "#7c2d12",
          950: "#431407",
        },
        success: {
          50: "#ecfdf5",
          400: "#34d399",
          500: "#10b981",
          600: "#059669",
        },
        warning: {
          50: "#fffbeb",
          400: "#fbbf24",
          500: "#f59e0b",
          600: "#d97706",
        },
        danger: {
          50: "#fef2f2",
          400: "#f87171",
          500: "#ef4444",
          600: "#dc2626",
        },
      },
      fontFamily: {
        sans: ["Inter", "Segoe UI", "Microsoft YaHei", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "Fira Code", "monospace"],
      },
      animation: {
        "fade-in": "fadeIn 0.3s ease-in-out",
        "slide-up": "slideUp 0.3s ease-out",
        "pulse-slow": "pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite",
      },
      keyframes: {
        fadeIn: {
          "0%": { opacity: "0" },
          "100%": { opacity: "1" },
        },
        slideUp: {
          "0%": { opacity: "0", transform: "translateY(10px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
      },
    },
  },
  plugins: [],
};

export default config;
