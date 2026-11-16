import { Link, Navigate, Route, Routes } from "react-router-dom";
import Admin from "./pages/Admin";
import Capture from "./pages/Capture";
import Cases from "./pages/Cases";
import Catalog from "./pages/Catalog";
import Chauffeur from "./pages/Chauffeur";
import Console from "./pages/Console";
import Corporate from "./pages/Corporate";
import Dispatch from "./pages/Dispatch";
import Geo from "./pages/Geo";
import Insights from "./pages/Insights";
import Integrations from "./pages/Integrations";
import Leads from "./pages/Leads";
import Partners from "./pages/Partners";
import Receipts from "./pages/Receipts";
import Growth from "./pages/Growth";
import Clients from "./pages/Clients";
import Referrals from "./pages/Referrals";
import Settings from "./pages/Settings";
import Studio from "./pages/Studio";
import Onboarding from "./pages/Onboarding";
import Track from "./pages/Track";
import Kyc from "./pages/Kyc";

export default function App() {
  return (
    <div className="shell">
      <nav className="nav">
        <span className="brand">Concierge Engine</span>
        <Link to="/capture">Book</Link>
        <Link to="/catalog">Catalog</Link>
        <Link to="/console">Concierge console</Link>
        <Link to="/cases">Cases</Link>
        <Link to="/kyc">KYC</Link>
        <Link to="/dispatch">Dispatch board</Link>
        <Link to="/partners">Partners</Link>
        <Link to="/geo">Geo</Link>
        <Link to="/receipts">Receipts &amp; SLA</Link>
        <Link to="/growth">Growth</Link>
        <Link to="/leads">Leads</Link>
        <Link to="/insights">Insights</Link>
        <Link to="/studio">Site Studio</Link>
        <Link to="/clients">Clients</Link>
        <Link to="/referrals">Referrals</Link>
        <Link to="/corporate">Corporate</Link>
        <Link to="/integrations">Integrations</Link>
        <Link to="/admin">Admin</Link>
        <Link to="/onboarding">Setup</Link>
        <Link to="/settings">Settings</Link>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Navigate to="/capture" replace />} />
          <Route path="/capture" element={<Capture />} />
          <Route path="/catalog" element={<Catalog />} />
          <Route path="/console" element={<Console />} />
          <Route path="/cases" element={<Cases />} />
          <Route path="/kyc" element={<Kyc />} />
          <Route path="/dispatch" element={<Dispatch />} />
          <Route path="/partners" element={<Partners />} />
          <Route path="/geo" element={<Geo />} />
          <Route path="/receipts" element={<Receipts />} />
          <Route path="/growth" element={<Growth />} />
          <Route path="/leads" element={<Leads />} />
          <Route path="/insights" element={<Insights />} />
          <Route path="/studio" element={<Studio />} />
          <Route path="/clients" element={<Clients />} />
          <Route path="/referrals" element={<Referrals />} />
          <Route path="/corporate" element={<Corporate />} />
          <Route path="/integrations" element={<Integrations />} />
          <Route path="/admin" element={<Admin />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/onboarding" element={<Onboarding />} />
          <Route path="/chauffeur" element={<Chauffeur />} />
          <Route path="/track" element={<Track />} />
        </Routes>
      </main>
    </div>
  );
}
