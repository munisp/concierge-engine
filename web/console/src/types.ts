export interface Tenant {
  id: string;
  slug: string;
  name: string;
  archetype: string;
  autonomy_dial: number;
  pricing_rules: { currency: string; deposit_pct: number; platform_fee_pct: number };
  sla_config?: { quote_sla_seconds: number; case_sla_hours?: Record<string, number> };
  tax_config?: { engine?: string; country?: string; vat_rate_bp?: number; prices_include_tax?: boolean };
  payment_config?: { disable_stripe?: boolean; disable_paystack?: boolean };
  reward_program?: { disabled?: boolean; referrer_pct?: number; referee_pct?: number; max_reward_minor?: number; expiry_days?: number; boost_until?: string; boost_multiplier?: number };
  onboarding_config?: { track?: string; skipped?: string[] };
  site_config?: {
    custom_domains?: string[]; google_verification?: string;
    locale?: string; social_links?: string[];
    locales?: string[]; brand_i18n?: Record<string, Record<string, string>>;
    theme?: { preset?: string; primary?: string; accent?: string; font?: string; corners?: string };
    hero_image_id?: string;
    design?: string; // classic | editorial
    hidden_sections?: string[];
    gallery_ids?: string[];
    content_blocks?: Record<string, Record<string, string>>; // Phase 54: slot -> locale -> text
  };
  outreach_config?: { marketing_cap_7d?: number };
  operator_config?: { alert_phones?: string[] };
  dispatch_config?: { turnaround_minutes?: number; default_duration_minutes?: number };
  lifecycle_config?: { disabled?: boolean; post_trip_hours?: number; win_back_days?: number; lead_nudge_days?: number };
  retention_config?: { disabled?: boolean; geo_stamps_days?: number; message_bodies_days?: number; attribution_days?: number };
  kyc_config?: { disabled?: boolean; standard_above_minor?: number; enhanced_above_minor?: number; case_expiry_days?: number };
  acquisition_config?: { disabled?: boolean; fee_pct?: number; channels?: string[];
    goal_clients?: number; goal_days?: number; cohort_started_at?: string };
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
  start_at?: string; // Phase 22 service window
  end_at?: string;
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

// Phase 31/32: catalog, media, fleet and crew records as served by the API.
export interface CatalogService {
  id: string;
  vertical: string;
  name: string;
  base_minor: number;
  currency: string;
  per_km_minor: number;
  per_hour_minor: number;
  auto_quotable: boolean;
  active: boolean;
  image_id?: string;
}

export interface Media {
  id: string;
  kind: string; // hero | service | misc
  content_type: string;
  size_bytes: number;
  created_at: string;
}

export interface FleetResource {
  id: string;
  kind: string;
  label: string;
  partner_id?: string;
  quality_score: number;
  active: boolean;
}

export interface CrewMember {
  id: string;
  full_name: string;
  certificates: string[];
  quality_score: number;
  active: boolean;
}
