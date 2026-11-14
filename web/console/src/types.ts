export interface Tenant {
  id: string;
  slug: string;
  name: string;
  archetype: string;
  autonomy_dial: number;
  pricing_rules: { currency: string; deposit_pct: number; platform_fee_pct: number };
}

export interface ServiceItem {
  vertical: string;
  pickup_address: string;
  pickup_lat?: number;
  pickup_lng?: number;
  dropoff_address?: string;
  dropoff_lat?: number;
  dropoff_lng?: number;
  pickup_at: number;
  passengers: number;
  vehicle_class?: string;
  hours?: number;
  notes?: string;
}

export interface ServiceRequest {
  id: string;
  tenant_id: string;
  client_id: string;
  state: string;
  version: number;
  items: ServiceItem[];
}

export interface Quote {
  id: string;
  request_id: string;
  origin: "INSTANT" | "HUMAN";
  total_minor: number;
  deposit_minor: number;
  currency: string;
  tax_minor?: number;
  tax_engine?: string;
  lines: { description: string; amount_minor: number }[];
  expires_at: string;
}

export interface HandoffItem {
  id: string;
  conversation_id: string;
  trigger: string;
  reason: string;
  brief: { reason: string; recent_messages: string[]; message_count: number };
  status: string;
  created_at: string;
}

export interface Assignment {
  id: string;
  booking_id: string;
  fleet_resource_id?: string;
  crew_member_id?: string;
  status: string;
  created_at: string;
}

export interface Message {
  id: string;
  author: "CLIENT" | "AI" | "HUMAN_STAFF" | "SYSTEM";
  body: string;
  sent_at: string;
}

export function money(minor: number, currency: string): string {
  return new Intl.NumberFormat("en-GB", { style: "currency", currency }).format(minor / 100);
}
