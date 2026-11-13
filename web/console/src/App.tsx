import { Link, Navigate, Route, Routes } from "react-router-dom";
import Admin from "./pages/Admin";
import Capture from "./pages/Capture";
import Console from "./pages/Console";
import Dispatch from "./pages/Dispatch";

export default function App() {
  return (
    <div className="shell">
      <nav className="nav">
        <span className="brand">Concierge Engine</span>
        <Link to="/capture">Book</Link>
        <Link to="/console">Concierge console</Link>
        <Link to="/dispatch">Dispatch board</Link>
        <Link to="/admin">Admin</Link>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Navigate to="/capture" replace />} />
          <Route path="/capture" element={<Capture />} />
          <Route path="/console" element={<Console />} />
          <Route path="/dispatch" element={<Dispatch />} />
          <Route path="/admin" element={<Admin />} />
        </Routes>
      </main>
    </div>
  );
}
