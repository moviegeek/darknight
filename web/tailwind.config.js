/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        // dark, cinematic palette — the library is mostly watched at night.
        bg: {
          DEFAULT: "#0b0d12",
          panel: "#141821",
          card: "#1a1f2b",
          hover: "#222836",
        },
        border: "#272e3d",
        ink: {
          DEFAULT: "#e6e9ef",
          muted: "#9aa3b2",
          dim: "#6b7384",
        },
        accent: {
          DEFAULT: "#e50914", // a film-reel red
          hover: "#ff1f2b",
        },
      },
    },
  },
  plugins: [],
};
