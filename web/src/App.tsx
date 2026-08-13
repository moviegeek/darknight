import { Link, NavLink, Route, Routes } from "react-router-dom";
import { Film, FolderTree, Layers, Settings as SettingsIcon, Terminal } from "lucide-react";

import CollectionPage from "./pages/CollectionPage";
import CollectionsPage from "./pages/CollectionsPage";
import LibraryPage from "./pages/LibraryPage";
import MoviePage from "./pages/MoviePage";
import SettingsPage from "./pages/SettingsPage";
import SqlConsolePage from "./pages/SqlConsolePage";
import { cn } from "./lib/format";

export default function App() {
  return (
    <div className="flex min-h-screen flex-col">
      <Header />
      <main className="flex-1">
        <Routes>
          <Route path="/" element={<LibraryPage />} />
          <Route path="/movie/:id" element={<MoviePage />} />
          <Route path="/collections" element={<CollectionsPage />} />
          <Route path="/collections/:id" element={<CollectionPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/dev/sql" element={<SqlConsolePage />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
    </div>
  );
}

function Header() {
  return (
    <header className="sticky top-0 z-20 border-b border-border bg-bg-panel/95 backdrop-blur">
      <div className="mx-auto flex max-w-[1600px] items-center gap-6 px-6 py-3">
        <Link to="/" className="flex items-center gap-2 text-ink">
          <Film className="h-5 w-5 text-accent" />
          <span className="text-lg font-semibold tracking-tight">MovieGeek</span>
        </Link>
        <nav className="flex items-center gap-1 text-sm">
          <NavTab to="/" icon={<FolderTree className="h-4 w-4" />} label="资料库" end />
          <NavTab to="/collections" icon={<Layers className="h-4 w-4" />} label="合集" />
          <NavTab to="/settings" icon={<SettingsIcon className="h-4 w-4" />} label="设置" />
          <NavTab to="/dev/sql" icon={<Terminal className="h-4 w-4" />} label="控制台" />
        </nav>
      </div>
    </header>
  );
}

function NavTab({
  to,
  icon,
  label,
  end,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
  end?: boolean;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          "flex items-center gap-1.5 rounded-md px-3 py-1.5 transition",
          isActive ? "bg-bg-card text-ink" : "text-ink-muted hover:text-ink"
        )
      }
    >
      {icon}
      {label}
    </NavLink>
  );
}

function NotFound() {
  return (
    <div className="mx-auto max-w-md py-24 text-center text-ink-muted">
      <p className="text-4xl font-bold text-ink">404</p>
      <p className="mt-2">页面不存在。</p>
    </div>
  );
}
