import React from "react";
import ReactDOM from "react-dom/client";
import { HashRouter, Routes, Route, Navigate } from "react-router-dom";
import App, { AuthGuard } from "./App";
import Dashboard from "./pages/Dashboard";
import Logs from "./pages/Logs";
import Keys from "./pages/Keys";
import Models from "./pages/Models";
import Settings from "./pages/Settings";
import "./index.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <AuthGuard>
      <HashRouter>
        <Routes>
          <Route path="/" element={<App />}>
            <Route index element={<Dashboard />} />
            <Route path="logs" element={<Logs />} />
            <Route path="logs/:id" element={<Logs />} />
            <Route path="keys" element={<Keys />} />
            <Route path="models" element={<Models />} />
            <Route path="settings" element={<Settings />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </HashRouter>
    </AuthGuard>
  </React.StrictMode>
);
