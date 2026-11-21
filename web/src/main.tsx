import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Shell } from "./Shell";
import "./styles.css";
import "./search.css";

// VITE_API_BASE_URL is for a UI served apart from the API (vite dev
// against a remote marquee); the embedded build leaves it empty.
const baseUrl: string = import.meta.env.VITE_API_BASE_URL ?? "";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Shell baseUrl={baseUrl} />
  </StrictMode>,
);
