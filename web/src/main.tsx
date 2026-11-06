import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { BrowserRouter, Navigate, Route, Routes } from "react-router"

import App from "./App"
import { I18nProvider } from "./i18n"
import "./index.css"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <I18nProvider>
        <Routes>
          <Route element={<App />}>
            <Route index />
            <Route path="organizations" />
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </I18nProvider>
    </BrowserRouter>
  </StrictMode>,
)
