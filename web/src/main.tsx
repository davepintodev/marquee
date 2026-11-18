import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { createApi } from "./api/client";
import "./styles.css";

// VITE_API_BASE_URL is for a UI served apart from the API (vite dev
// against a remote marquee); the embedded build leaves it empty.
const api = createApi({ baseUrl: import.meta.env.VITE_API_BASE_URL ?? "" });

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App api={api} />
  </StrictMode>,
);
