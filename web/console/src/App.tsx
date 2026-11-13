import { Link, Navigate, Route, Routes } from "react-router-dom";
import Admin from "./pages/Admin";
import Capture from "./pages/Capture";
import Cases from "./pages/Cases";
import Chauffeur from "./pages/Chauffeur";
import Console from "./pages/Console";
import Corporate from "./pages/Corporate";
import Dispatch from "./pages/Dispatch";
import Integrations from "./pages/Integrations";
import Partners from "./pages/Partners";
import Referrals from "./pages/Referrals";

export default function App() {
  return (
    <div className="shell">
      <nav className="nav">
        <span className="brand">Concierge Engine</span>
        <Link to="/capture">Book</Link>
        <Link to="/console">Concierge console</Link>
        <Link to="/cases">Cases</Link>
        <Link to="/dispatch">Dispatch board</Link>
        <Link to="/partners">Partners</Link>
        <Link to="/referrals">Referrals</Link>
        <Link to="/corporate">Corporate</Link>
        <Link to="/integrations">Integrations</Link>
        <Link to="/admin">Admin</Link>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Navigate to="/capture" replace />} />
          <Route path="/capture" element={<Capture />} />
          <Route path="/console" element={<Console />} />
          <Route path="/cases" element={<Cases />} />
          <Route path="/dispatch" element={<Dispatch />} />
          <Route path="/partners" element={<Partners />} />
          <Route path="/referrals" element={<Referrals />} />
          <Route path="/corporate" element={<Corporate />} />
          <Route path="/integrations" element={<Integrations />} />
          <Route path="/admin" element={<Admin />} />
          <Route path="/chauffeur" element={<Chauffeur />} />
        </Routes>
      </main>
    </div>
  );
}
