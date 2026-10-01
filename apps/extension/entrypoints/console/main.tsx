// CTM-EXT-030 Full-tab console: the same React UI as the desktop app (shared from apps/web).
// The Controller URL comes from the side panel setting (chrome.storage) and is handed to the web UI.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "../../../web/src/App";
import { setController, token } from "../../../web/src/api";
import "../../../web/src/styles.css";

browser.storage.local.get("controllerUrl").then((r) => {
  setController((r.controllerUrl as string) || "http://127.0.0.1:8080", token());
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
});
