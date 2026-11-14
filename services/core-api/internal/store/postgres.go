//go:build pg

// Postgres store (build with: go build -tags pg). Enforces tenant isolation
// via row-level security: every transaction sets app.tenant_id, and the RLS
// policies in db/migrations/0001_init.sql do the rest (Master Spec §13).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type postgres struct{ db *sql.DB }

func NewPostgres(dsn string) (Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &postgres{db: db}, nil
}

func (p *postgres) Close() { _ = p.db.Close() }

// tenantTx opens a transaction scoped to the tenant for RLS enforcement.
func (p *postgres) tenantTx(ctx context.Context, tenantID string) (*sql.Tx, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func notFound(err error) error {
	if err == sql.ErrNoRows {
		return httpx.ErrNotFound
	}
	return err
}

func (p *postgres) Tenants() TenantStore             { return pgTenants{p} }
func (p *postgres) Clients() ClientStore             { return pgClients{p} }
func (p *postgres) Catalog() CatalogStore            { return pgCatalog{p} }
func (p *postgres) Requests() RequestStore           { return pgRequests{p} }
func (p *postgres) Quotes() QuoteStore               { return pgQuotes{p} }
func (p *postgres) Bookings() BookingStore           { return pgBookings{p} }
func (p *postgres) Dispatch() DispatchStore          { return pgDispatch{p} }
func (p *postgres) Payments() PaymentStore           { return pgPayments{p} }
func (p *postgres) Conversations() ConversationStore { return pgConversations{p} }
func (p *postgres) Handoffs() HandoffStore           { return pgHandoffs{p} }
func (p *postgres) Outbox() OutboxStore              { return pgOutbox{p} }
func (p *postgres) Attribution() AttributionStore    { return pgAttribution{p} }
func (p *postgres) Outreach() OutreachStore          { return pgOutreach{p} }

// ------------------------------------------------------------- tenants
type pgTenants struct{ p *postgres }

func (s pgTenants) Create(ctx context.Context, t *model.Tenant) error {
	brand, _ := json.Marshal(t.Brand)
	pricing, _ := json.Marshal(t.PricingRules)
	sla, _ := json.Marshal(t.SLAConfig)
	taxCfg, _ := json.Marshal(t.TaxConfig)
	payCfg, _ := json.Marshal(t.PaymentConfig)
	rewardCfg, _ := json.Marshal(t.RewardProgram)
	outreachCfg, _ := json.Marshal(t.OutreachConfig)
	operatorCfg, _ := json.Marshal(t.OperatorConfig)
	dispatchCfg, _ := json.Marshal(t.DispatchConfig)
	lifecycleCfg, _ := json.Marshal(t.LifecycleConfig)
	acqCfg, _ := json.Marshal(t.AcquisitionConfig)
	obCfg, _ := json.Marshal(t.OnboardingConfig)
	siteCfg, _ := json.Marshal(t.SiteConfig)
	_, err := s.p.db.ExecContext(ctx,
		`INSERT INTO tenants (id, slug, name, archetype, brand, pricing_rules, sla_config, autonomy_dial, tier2_limit_minor, tax_config, payment_config, reward_program, outreach_config, operator_config, dispatch_config, lifecycle_config, acquisition_config, onboarding_config, site_config)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		t.ID, t.Slug, t.Name, t.Archetype, brand, pricing, sla, t.AutonomyDial, t.Tier2LimitMinor, taxCfg, payCfg, rewardCfg, outreachCfg, operatorCfg, dispatchCfg, lifecycleCfg, acqCfg, obCfg, siteCfg)
	return err
}

func (s pgTenants) scan(row *sql.Row) (*model.Tenant, error) {
	var t model.Tenant
	var brand, pricing, sla, taxCfg, payCfg, rewardCfg, outreachCfg, operatorCfg, dispatchCfg, lifecycleCfg, acqCfg, obCfg, siteCfg []byte
	err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Archetype, &brand, &pricing, &sla,
		&t.AutonomyDial, &t.Tier2LimitMinor, &taxCfg, &payCfg, &rewardCfg, &outreachCfg, &operatorCfg, &dispatchCfg, &lifecycleCfg, &acqCfg, &obCfg, &siteCfg, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal(brand, &t.Brand)
	_ = json.Unmarshal(pricing, &t.PricingRules)
	_ = json.Unmarshal(sla, &t.SLAConfig)
	_ = json.Unmarshal(taxCfg, &t.TaxConfig)
	_ = json.Unmarshal(payCfg, &t.PaymentConfig)
	_ = json.Unmarshal(rewardCfg, &t.RewardProgram)
	_ = json.Unmarshal(outreachCfg, &t.OutreachConfig)
	_ = json.Unmarshal(operatorCfg, &t.OperatorConfig)
	_ = json.Unmarshal(dispatchCfg, &t.DispatchConfig)
	_ = json.Unmarshal(lifecycleCfg, &t.LifecycleConfig)
	_ = json.Unmarshal(acqCfg, &t.AcquisitionConfig)
	_ = json.Unmarshal(obCfg, &t.OnboardingConfig)
	_ = json.Unmarshal(siteCfg, &t.SiteConfig)
	return &t, nil
}

func (s pgTenants) Get(ctx context.Context, tenantID string) (*model.Tenant, error) {
	return s.scan(s.p.db.QueryRowContext(ctx,
		`SELECT id, slug, name, archetype, brand, pricing_rules, sla_config, autonomy_dial, tier2_limit_minor, tax_config, payment_config, reward_program, outreach_config, operator_config, dispatch_config, lifecycle_config, acquisition_config, onboarding_config, site_config, created_at
		 FROM tenants WHERE id = $1`, tenantID))
}

func (s pgTenants) GetBySlug(ctx context.Context, slug string) (*model.Tenant, error) {
	return s.scan(s.p.db.QueryRowContext(ctx,
		`SELECT id, slug, name, archetype, brand, pricing_rules, sla_config, autonomy_dial, tier2_limit_minor, tax_config, payment_config, reward_program, outreach_config, operator_config, dispatch_config, lifecycle_config, acquisition_config, onboarding_config, site_config, created_at
		 FROM tenants WHERE slug = $1`, slug))
}

// GetByDomain (Phase 28): jsonb array containment on site_config.custom_domains.
// Host normalization (lowercase, port/www strip) happens in the caller.
func (s pgTenants) GetByDomain(ctx context.Context, host string) (*model.Tenant, error) {
	return s.scan(s.p.db.QueryRowContext(ctx,
		`SELECT id, slug, name, archetype, brand, pricing_rules, sla_config, autonomy_dial, tier2_limit_minor, tax_config, payment_config, reward_program, outreach_config, operator_config, dispatch_config, lifecycle_config, acquisition_config, onboarding_config, site_config, created_at
		 FROM tenants WHERE site_config->'custom_domains' ? $1`, host))
}

func (s pgTenants) UpdateConfig(ctx context.Context, t *model.Tenant) error {
	brand, _ := json.Marshal(t.Brand)
	pricing, _ := json.Marshal(t.PricingRules)
	sla, _ := json.Marshal(t.SLAConfig)
	taxCfg, _ := json.Marshal(t.TaxConfig)
	payCfg, _ := json.Marshal(t.PaymentConfig)
	rewardCfg, _ := json.Marshal(t.RewardProgram)
	outreachCfg, _ := json.Marshal(t.OutreachConfig)
	operatorCfg, _ := json.Marshal(t.OperatorConfig)
	dispatchCfg, _ := json.Marshal(t.DispatchConfig)
	lifecycleCfg, _ := json.Marshal(t.LifecycleConfig)
	acqCfg, _ := json.Marshal(t.AcquisitionConfig)
	obCfg, _ := json.Marshal(t.OnboardingConfig)
	siteCfg, _ := json.Marshal(t.SiteConfig)
	res, err := s.p.db.ExecContext(ctx,
		`UPDATE tenants SET name=$2, brand=$3, pricing_rules=$4, sla_config=$5, autonomy_dial=$6, tier2_limit_minor=$7, tax_config=$8, payment_config=$9, reward_program=$10, outreach_config=$11, operator_config=$12, dispatch_config=$13, lifecycle_config=$14, acquisition_config=$15, onboarding_config=$16, site_config=$17
		 WHERE id=$1`, t.ID, t.Name, brand, pricing, sla, t.AutonomyDial, t.Tier2LimitMinor, taxCfg, payCfg, rewardCfg, outreachCfg, operatorCfg, dispatchCfg, lifecycleCfg, acqCfg, obCfg, siteCfg)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return nil
}

func (s pgTenants) List(ctx context.Context) ([]model.Tenant, error) {
	rows, err := s.p.db.QueryContext(ctx,
		`SELECT id, slug, name, archetype, brand, pricing_rules, sla_config, autonomy_dial, tier2_limit_minor, tax_config, payment_config, reward_program, outreach_config, operator_config, dispatch_config, lifecycle_config, acquisition_config, onboarding_config, site_config, created_at
		 FROM tenants ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Tenant{}
	for rows.Next() {
		var t model.Tenant
		var brand, pricing, sla, taxCfg, payCfg, rewardCfg, outreachCfg, operatorCfg, dispatchCfg, lifecycleCfg, acqCfg, obCfg, siteCfg []byte
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Archetype, &brand, &pricing, &sla,
			&t.AutonomyDial, &t.Tier2LimitMinor, &taxCfg, &payCfg, &rewardCfg, &outreachCfg, &operatorCfg, &dispatchCfg, &lifecycleCfg, &acqCfg, &obCfg, &siteCfg, &t.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(brand, &t.Brand)
		_ = json.Unmarshal(pricing, &t.PricingRules)
		_ = json.Unmarshal(sla, &t.SLAConfig)
		_ = json.Unmarshal(taxCfg, &t.TaxConfig)
		_ = json.Unmarshal(payCfg, &t.PaymentConfig)
		_ = json.Unmarshal(rewardCfg, &t.RewardProgram)
		_ = json.Unmarshal(outreachCfg, &t.OutreachConfig)
		_ = json.Unmarshal(operatorCfg, &t.OperatorConfig)
		_ = json.Unmarshal(dispatchCfg, &t.DispatchConfig)
		_ = json.Unmarshal(lifecycleCfg, &t.LifecycleConfig)
		_ = json.Unmarshal(acqCfg, &t.AcquisitionConfig)
		_ = json.Unmarshal(obCfg, &t.OnboardingConfig)
		_ = json.Unmarshal(siteCfg, &t.SiteConfig)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------- clients
type pgClients struct{ p *postgres }

func (s pgClients) Upsert(ctx context.Context, c *model.Client) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	consent, _ := json.Marshal(c.Consent)
	// vip_flags is text[]: database/sql can't bind []string through pgx's
	// stdlib shim, so cross over as jsonb and rebuild the array server-side.
	flags := []byte("[]")
	if len(c.VIPFlags) > 0 {
		flags, _ = json.Marshal(c.VIPFlags)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO clients (id, tenant_id, full_name, email, phone, org_id, vip_flags, loyalty_tier, kyc_status, consent, locale, timezone, erased_at)
		 VALUES ($1,$2,$3,$4,$5,nullif($6,'')::uuid,
		         coalesce((SELECT array_agg(x) FROM jsonb_array_elements_text($7::jsonb) AS x), '{}'::text[]),
		         $8,$9,$10,$11,$12,$13)
		 ON CONFLICT (id) DO UPDATE SET full_name=$3, email=$4, phone=$5,
			vip_flags=coalesce((SELECT array_agg(x) FROM jsonb_array_elements_text($7::jsonb) AS x), '{}'::text[]),
			loyalty_tier=$8, kyc_status=$9, consent=$10, locale=$11, timezone=$12, erased_at=$13`,
		c.ID, c.TenantID, c.FullName, c.Email, c.Phone, c.OrgID, string(flags), c.LoyaltyTier, c.KYCStatus, consent,
		c.Locale, c.Timezone, nilIfZero(c.ErasedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgClients) scanClient(row interface{ Scan(...any) error }) (*model.Client, error) {
	var c model.Client
	var consent, flags []byte
	var org *string
	var erasedAt sql.NullTime
	err := row.Scan(&c.ID, &c.TenantID, &c.FullName, &c.Email, &c.Phone, &org,
		&flags, &c.LoyaltyTier, &c.KYCStatus, &consent, &c.Locale, &c.Timezone, &erasedAt, &c.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if org != nil {
		c.OrgID = *org
	}
	if erasedAt.Valid {
		c.ErasedAt = erasedAt.Time
	}
	_ = json.Unmarshal(flags, &c.VIPFlags)
	_ = json.Unmarshal(consent, &c.Consent)
	return &c, nil
}

const clientCols = `id, tenant_id, coalesce(full_name,''), coalesce(email,''), coalesce(phone,''), org_id::text,
	coalesce(to_jsonb(vip_flags), '[]'::jsonb), coalesce(loyalty_tier,''), kyc_status, consent, locale, timezone, erased_at, created_at`

func (s pgClients) Get(ctx context.Context, tenantID, id string) (*model.Client, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := s.scanClient(tx.QueryRowContext(ctx,
		`SELECT `+clientCols+` FROM clients WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgClients) FindByPhone(ctx context.Context, tenantID, phone string) (*model.Client, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := s.scanClient(tx.QueryRowContext(ctx,
		`SELECT `+clientCols+` FROM clients WHERE phone=$1 LIMIT 1`, phone))
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgClients) FindByEmail(ctx context.Context, tenantID, email string) (*model.Client, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := s.scanClient(tx.QueryRowContext(ctx,
		`SELECT `+clientCols+` FROM clients WHERE lower(email)=lower($1) LIMIT 1`, email))
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgClients) SavePreference(ctx context.Context, pr *model.Preference) error {
	tx, err := s.p.tenantTx(ctx, pr.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO preferences (id, tenant_id, client_id, key, value, consent_scope, source_quote, recorded_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (tenant_id, client_id, key) DO UPDATE SET value=$5, consent_scope=$6, source_quote=$7, recorded_by=$8, recorded_at=now()`,
		pr.ID, pr.TenantID, pr.ClientID, pr.Key, pr.Value, pr.ConsentScope, pr.SourceQuote, pr.RecordedBy)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgClients) Preferences(ctx context.Context, tenantID, clientID string) ([]model.Preference, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, client_id, key, value, consent_scope, coalesce(source_quote,''), recorded_by, recorded_at
		 FROM preferences WHERE client_id=$1 ORDER BY key`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Preference{}
	for rows.Next() {
		var pr model.Preference
		if err := rows.Scan(&pr.ID, &pr.TenantID, &pr.ClientID, &pr.Key, &pr.Value,
			&pr.ConsentScope, &pr.SourceQuote, &pr.RecordedBy, &pr.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgClients) DeletePreferences(ctx context.Context, tenantID, clientID string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM preferences WHERE client_id=$1`, clientID); err != nil {
		return err
	}
	return tx.Commit()
}

// ------------------------------------------------------------- catalog
type pgCatalog struct{ p *postgres }

func (s pgCatalog) Add(ctx context.Context, svc *model.CatalogService) error {
	tx, err := s.p.tenantTx(ctx, svc.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	nameI18n, _ := json.Marshal(svc.NameI18n)
	descI18n, _ := json.Marshal(svc.DescI18n)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO catalog_services (id, tenant_id, vertical, name, base_minor, currency, per_km_minor, per_hour_minor, auto_quotable, active, name_i18n, desc_i18n)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		svc.ID, svc.TenantID, svc.Vertical, svc.Name, svc.BaseMinor, svc.Currency,
		svc.PerKMMinor, svc.PerHourMinor, svc.AutoQuotable, svc.Active, nameI18n, descI18n)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgCatalog) Update(ctx context.Context, svc *model.CatalogService) error {
	tx, err := s.p.tenantTx(ctx, svc.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	nameI18n, _ := json.Marshal(svc.NameI18n)
	descI18n, _ := json.Marshal(svc.DescI18n)
	res, err := tx.ExecContext(ctx,
		`UPDATE catalog_services SET vertical=$3, name=$4, base_minor=$5, currency=$6,
		 per_km_minor=$7, per_hour_minor=$8, auto_quotable=$9, active=$10,
		 name_i18n=$11, desc_i18n=$12
		 WHERE id=$1 AND tenant_id=$2`,
		svc.ID, svc.TenantID, svc.Vertical, svc.Name, svc.BaseMinor, svc.Currency,
		svc.PerKMMinor, svc.PerHourMinor, svc.AutoQuotable, svc.Active, nameI18n, descI18n)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgCatalog) List(ctx context.Context, tenantID string) ([]model.CatalogService, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, vertical, name, base_minor, currency, per_km_minor, per_hour_minor, auto_quotable, active, name_i18n, desc_i18n
		 FROM catalog_services WHERE active ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CatalogService{}
	for rows.Next() {
		var svc model.CatalogService
		var nameI18n, descI18n []byte
		if err := rows.Scan(&svc.ID, &svc.TenantID, &svc.Vertical, &svc.Name, &svc.BaseMinor,
			&svc.Currency, &svc.PerKMMinor, &svc.PerHourMinor, &svc.AutoQuotable, &svc.Active,
			&nameI18n, &descI18n); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(nameI18n, &svc.NameI18n)
		_ = json.Unmarshal(descI18n, &svc.DescI18n)
		// jsonb "null" (marshaled from a nil map) reads back as nil —
		// normalize so callers always see an empty map, matching memory.
		if svc.NameI18n == nil {
			svc.NameI18n = map[string]string{}
		}
		if svc.DescI18n == nil {
			svc.DescI18n = map[string]string{}
		}
		out = append(out, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- requests
type pgRequests struct{ p *postgres }

func (s pgRequests) Create(ctx context.Context, r *model.ServiceRequest) error {
	tx, err := s.p.tenantTx(ctx, r.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM service_requests WHERE idempotency_key=$1`, r.IdempotencyKey).Scan(&existing)
	if err == nil {
		// idempotent replay: load and return the original
		row := tx.QueryRowContext(ctx,
			`SELECT id, tenant_id, client_id, coalesce(conversation_id::text,''), state, version, idempotency_key, created_at, updated_at
			 FROM service_requests WHERE id=$1`, existing)
		if err := row.Scan(&r.ID, &r.TenantID, &r.ClientID, &r.ConversationID, &r.State,
			&r.Version, &r.IdempotencyKey, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO service_requests (id, tenant_id, client_id, conversation_id, state, version, idempotency_key)
		 VALUES ($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7)`,
		r.ID, r.TenantID, r.ClientID, r.ConversationID, string(r.State), r.Version, r.IdempotencyKey)
	if err != nil {
		return err
	}
	for _, item := range r.Items {
		payload, _ := json.Marshal(item)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO service_items (id, tenant_id, request_id, vertical, payload) VALUES ($1,$2,$3,$4,$5)`,
			item.ID, r.TenantID, r.ID, item.Vertical, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s pgRequests) Get(ctx context.Context, tenantID, id string) (*model.ServiceRequest, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var r model.ServiceRequest
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, client_id, coalesce(conversation_id::text,''), state, version, idempotency_key, created_at, updated_at
		 FROM service_requests WHERE id=$1`, id).
		Scan(&r.ID, &r.TenantID, &r.ClientID, &r.ConversationID, &r.State,
			&r.Version, &r.IdempotencyKey, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT payload FROM service_items WHERE request_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item model.ServiceItem
		if err := json.Unmarshal(payload, &item); err != nil {
			return nil, err
		}
		r.Items = append(r.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &r, tx.Commit()
}

func (s pgRequests) Update(ctx context.Context, r *model.ServiceRequest) error {
	tx, err := s.p.tenantTx(ctx, r.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE service_requests SET state=$2, version=$3, updated_at=now() WHERE id=$1`,
		r.ID, string(r.State), r.Version)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_items WHERE request_id=$1`, r.ID); err != nil {
		return err
	}
	for _, item := range r.Items {
		payload, _ := json.Marshal(item)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO service_items (id, tenant_id, request_id, vertical, payload) VALUES ($1,$2,$3,$4,$5)`,
			item.ID, r.TenantID, r.ID, item.Vertical, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ------------------------------------------------------------- quotes
type pgQuotes struct{ p *postgres }

func (s pgQuotes) Create(ctx context.Context, q *model.Quote) error {
	tx, err := s.p.tenantTx(ctx, q.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lines, _ := json.Marshal(q.Lines)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO quotes (id, tenant_id, request_id, version, origin, total_minor, deposit_minor, currency, tax_minor, tax_engine, lines, expires_at, policy_verdict_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		q.ID, q.TenantID, q.RequestID, q.Version, q.Origin, q.TotalMinor, q.DepositMinor,
		q.Currency, q.TaxMinor, q.TaxEngine, lines, nilIfZero(q.ExpiresAt), q.PolicyVerdictID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgQuotes) scan(row *sql.Row) (*model.Quote, error) {
	var q model.Quote
	var lines []byte
	var expiresAt sql.NullTime
	err := row.Scan(&q.ID, &q.TenantID, &q.RequestID, &q.Version, &q.Origin, &q.TotalMinor,
		&q.DepositMinor, &q.Currency, &q.TaxMinor, &q.TaxEngine, &lines, &expiresAt, &q.PolicyVerdictID, &q.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if expiresAt.Valid {
		q.ExpiresAt = expiresAt.Time
	}
	_ = json.Unmarshal(lines, &q.Lines)
	return &q, nil
}

const quoteCols = `id, tenant_id, request_id, version, origin, total_minor, deposit_minor, currency, tax_minor, tax_engine, lines, expires_at, coalesce(policy_verdict_id,''), created_at`

func (s pgQuotes) Get(ctx context.Context, tenantID, id string) (*model.Quote, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q, err := s.scan(tx.QueryRowContext(ctx, `SELECT `+quoteCols+` FROM quotes WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return q, tx.Commit()
}

func (s pgQuotes) LatestForRequest(ctx context.Context, tenantID, requestID string) (*model.Quote, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q, err := s.scan(tx.QueryRowContext(ctx,
		`SELECT `+quoteCols+` FROM quotes WHERE request_id=$1 ORDER BY version DESC LIMIT 1`, requestID))
	if err != nil {
		return nil, err
	}
	return q, tx.Commit()
}

// ------------------------------------------------------------- bookings
type pgBookings struct{ p *postgres }

func (s pgBookings) Create(ctx context.Context, b *model.Booking) error {
	tx, err := s.p.tenantTx(ctx, b.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO bookings (id, tenant_id, request_id, quote_id, state) VALUES ($1,$2,$3,$4,$5)`,
		b.ID, b.TenantID, b.RequestID, b.QuoteID, b.State)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgBookings) Get(ctx context.Context, tenantID, id string) (*model.Booking, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var b model.Booking
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, request_id, quote_id, state, created_at FROM bookings WHERE id=$1`, id).
		Scan(&b.ID, &b.TenantID, &b.RequestID, &b.QuoteID, &b.State, &b.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &b, tx.Commit()
}

func (s pgBookings) UpdateState(ctx context.Context, tenantID, id, state string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE bookings SET state=$2 WHERE id=$1`, id, state)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

// ------------------------------------------------------------- dispatch
type pgDispatch struct{ p *postgres }

func (s pgDispatch) AddFleetResource(ctx context.Context, f *model.FleetResource) error {
	tx, err := s.p.tenantTx(ctx, f.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO fleet_resources (id, tenant_id, kind, label, partner_id, quality_score, active)
		 VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,$6,$7)`,
		f.ID, f.TenantID, f.Kind, f.Label, f.PartnerID, f.QualityScore, f.Active)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgDispatch) ListFleet(ctx context.Context, tenantID string) ([]model.FleetResource, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, kind, label, coalesce(partner_id::text,''), quality_score, active FROM fleet_resources ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.FleetResource{}
	for rows.Next() {
		var f model.FleetResource
		if err := rows.Scan(&f.ID, &f.TenantID, &f.Kind, &f.Label, &f.PartnerID, &f.QualityScore, &f.Active); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgDispatch) AddCrewMember(ctx context.Context, c *model.CrewMember) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	certs, _ := json.Marshal(c.Certificates)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO crew_members (id, tenant_id, full_name, certificates, quality_score, active)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		c.ID, c.TenantID, c.FullName, certs, c.QualityScore, c.Active)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgDispatch) ListCrew(ctx context.Context, tenantID string) ([]model.CrewMember, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, full_name, certificates, quality_score, active FROM crew_members ORDER BY full_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CrewMember{}
	for rows.Next() {
		var c model.CrewMember
		var certs []byte
		if err := rows.Scan(&c.ID, &c.TenantID, &c.FullName, &certs, &c.QualityScore, &c.Active); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(certs, &c.Certificates)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgDispatch) CreateAssignment(ctx context.Context, a *model.Assignment) error {
	tx, err := s.p.tenantTx(ctx, a.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO assignments (id, tenant_id, booking_id, fleet_resource_id, crew_member_id, partner_id, status, sla_deadline, start_at, end_at)
		 VALUES ($1,$2,$3,nullif($4,'')::uuid,nullif($5,'')::uuid,nullif($6,'')::uuid,$7,$8,$9,$10)`,
		a.ID, a.TenantID, a.BookingID, a.FleetResourceID, a.CrewMemberID, a.PartnerID, a.Status,
		nilIfZero(a.SLADeadline), nilIfZero(a.StartAt), nilIfZero(a.EndAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgDispatch) UpdateAssignmentStatus(ctx context.Context, tenantID, id, status string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE assignments SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgDispatch) ListAssignments(ctx context.Context, tenantID string) ([]model.Assignment, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, booking_id, coalesce(fleet_resource_id::text,''), coalesce(crew_member_id::text,''),
		        coalesce(partner_id::text,''), status, sla_deadline, start_at, end_at, created_at
		 FROM assignments ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Assignment{}
	for rows.Next() {
		var a model.Assignment
		var sla, startAt, endAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.TenantID, &a.BookingID, &a.FleetResourceID, &a.CrewMemberID,
			&a.PartnerID, &a.Status, &sla, &startAt, &endAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.SLADeadline = sla.Time
		a.StartAt = startAt.Time
		a.EndAt = endAt.Time
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgDispatch) AddGeoStamp(ctx context.Context, g *model.GeoStamp) error {
	tx, err := s.p.tenantTx(ctx, g.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO geostamps (id, tenant_id, assignment_id, kind, lat, lng, accuracy_m) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		g.ID, g.TenantID, g.AssignmentID, g.Kind, g.Lat, g.Lng, g.AccuracyM)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgDispatch) LatestGeoStamp(ctx context.Context, tenantID, assignmentID string) (*model.GeoStamp, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var g model.GeoStamp
	var acc sql.NullFloat64
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, assignment_id, kind, lat, lng, accuracy_m, at FROM geostamps
		 WHERE assignment_id=$1 ORDER BY at DESC LIMIT 1`, assignmentID).
		Scan(&g.ID, &g.TenantID, &g.AssignmentID, &g.Kind, &g.Lat, &g.Lng, &acc, &g.At)
	if err != nil {
		return nil, notFound(err)
	}
	g.AccuracyM = acc.Float64
	return &g, nil
}

// ------------------------------------------------------------- payments
type pgPayments struct{ p *postgres }

func (s pgPayments) CreateIntent(ctx context.Context, pi *model.PaymentIntent) error {
	tx, err := s.p.tenantTx(ctx, pi.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM payment_intents WHERE idempotency_key=$1`, pi.IdempotencyKey).Scan(&existing)
	if err == nil {
		row := tx.QueryRowContext(ctx,
			`SELECT id, tenant_id, coalesce(booking_id::text,''), coalesce(quote_id::text,''), mode, amount_minor,
			        currency, psp, coalesce(psp_ref,''), coalesce(hosted_url,''), state, idempotency_key, reward_applied_minor, created_at
			 FROM payment_intents WHERE id=$1`, existing)
		if err := row.Scan(&pi.ID, &pi.TenantID, &pi.BookingID, &pi.QuoteID, &pi.Mode, &pi.AmountMinor,
			&pi.Currency, &pi.PSP, &pi.PSPRef, &pi.HostedURL, &pi.State, &pi.IdempotencyKey,
			&pi.RewardAppliedMinor); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO payment_intents (id, tenant_id, booking_id, quote_id, mode, amount_minor, currency, psp, hosted_url, state, idempotency_key, reward_applied_minor)
		 VALUES ($1,$2,nullif($3,'')::uuid,nullif($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,$12)`,
		pi.ID, pi.TenantID, pi.BookingID, pi.QuoteID, pi.Mode, pi.AmountMinor, pi.Currency,
		pi.PSP, pi.HostedURL, pi.State, pi.IdempotencyKey, pi.RewardAppliedMinor)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgPayments) GetIntent(ctx context.Context, tenantID, id string) (*model.PaymentIntent, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var pi model.PaymentIntent
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, coalesce(booking_id::text,''), coalesce(quote_id::text,''), mode, amount_minor,
		        currency, psp, coalesce(psp_ref,''), coalesce(hosted_url,''), state, idempotency_key, reward_applied_minor, created_at
		 FROM payment_intents WHERE id=$1`, id).
		Scan(&pi.ID, &pi.TenantID, &pi.BookingID, &pi.QuoteID, &pi.Mode, &pi.AmountMinor,
			&pi.Currency, &pi.PSP, &pi.PSPRef, &pi.HostedURL, &pi.State, &pi.IdempotencyKey,
			&pi.RewardAppliedMinor, &pi.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &pi, tx.Commit()
}

func (s pgPayments) UpdateIntentState(ctx context.Context, tenantID, id, state, pspRef string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE payment_intents SET state=$2, psp_ref=coalesce(nullif($3,''), psp_ref) WHERE id=$1`, id, state, pspRef)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgPayments) AddPosting(ctx context.Context, lp *model.LedgerPosting) error {
	tx, err := s.p.tenantTx(ctx, lp.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO ledger_postings (id, tenant_id, booking_id, debit_code, credit_code, amount_minor, currency, tb_transfer_id, idempotency_key)
		 VALUES ($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		lp.ID, lp.TenantID, lp.BookingID, lp.DebitCode, lp.CreditCode, lp.AmountMinor,
		lp.Currency, lp.TBTransferID, lp.IdempotencyKey)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgPayments) Postings(ctx context.Context, tenantID string) ([]model.LedgerPosting, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, coalesce(booking_id::text,''), debit_code, credit_code, amount_minor, currency,
		        coalesce(tb_transfer_id,''), idempotency_key, created_at FROM ledger_postings ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.LedgerPosting{}
	for rows.Next() {
		var lp model.LedgerPosting
		if err := rows.Scan(&lp.ID, &lp.TenantID, &lp.BookingID, &lp.DebitCode, &lp.CreditCode,
			&lp.AmountMinor, &lp.Currency, &lp.TBTransferID, &lp.IdempotencyKey, &lp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, lp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- conversations
type pgConversations struct{ p *postgres }

func (s pgConversations) CreateConversation(ctx context.Context, c *model.Conversation) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO conversations (id, tenant_id, client_id, channel, state, external_ref)
		 VALUES ($1,$2,nullif($3,'')::uuid,$4,$5,$6)`,
		c.ID, c.TenantID, c.ClientID, c.Channel, c.State, c.ExternalRef)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgConversations) GetConversation(ctx context.Context, tenantID, id string) (*model.Conversation, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var c model.Conversation
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, coalesce(client_id::text,''), channel, state, coalesce(external_ref,''), created_at
		 FROM conversations WHERE id=$1`, id).
		Scan(&c.ID, &c.TenantID, &c.ClientID, &c.Channel, &c.State, &c.ExternalRef, &c.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, tx.Commit()
}

func (s pgConversations) FindActive(ctx context.Context, tenantID, clientID, channel string) (*model.Conversation, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var c model.Conversation
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, coalesce(client_id::text,''), channel, state, coalesce(external_ref,''), created_at
		 FROM conversations
		 WHERE client_id=$1 AND channel=$2 AND state<>'CLOSED'
		 ORDER BY created_at DESC LIMIT 1`, clientID, channel).
		Scan(&c.ID, &c.TenantID, &c.ClientID, &c.Channel, &c.State, &c.ExternalRef, &c.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, tx.Commit()
}

func (s pgConversations) SetState(ctx context.Context, tenantID, id, state string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE conversations SET state=$2 WHERE id=$1`, id, state)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgConversations) AppendMessage(ctx context.Context, m *model.Message) error {
	tx, err := s.p.tenantTx(ctx, m.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO messages (id, tenant_id, conversation_id, author, body, policy_verdict_id)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		m.ID, m.TenantID, m.ConversationID, m.Author, m.Body, m.PolicyVerdictID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgConversations) Timeline(ctx context.Context, tenantID, conversationID string) ([]model.Message, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, conversation_id, author, body, coalesce(policy_verdict_id,''), sent_at
		 FROM messages WHERE conversation_id=$1 ORDER BY sent_at`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Message{}
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.TenantID, &m.ConversationID, &m.Author, &m.Body,
			&m.PolicyVerdictID, &m.SentAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- handoffs
type pgHandoffs struct{ p *postgres }

func (s pgHandoffs) Open(ctx context.Context, h *model.HandoffItem) error {
	tx, err := s.p.tenantTx(ctx, h.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	brief, _ := json.Marshal(h.Brief)
	pending, _ := json.Marshal(h.PendingAction)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO handoff_queue (id, tenant_id, conversation_id, trigger_kind, reason, brief, pending_action, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		h.ID, h.TenantID, h.ConversationID, h.Trigger, h.Reason, brief, pending, h.Status)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgHandoffs) Get(ctx context.Context, tenantID, id string) (*model.HandoffItem, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var h model.HandoffItem
	var brief, pending []byte
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, conversation_id, trigger_kind, reason, brief, pending_action, status, coalesce(owner,''), created_at
		 FROM handoff_queue WHERE id=$1`, id).
		Scan(&h.ID, &h.TenantID, &h.ConversationID, &h.Trigger, &h.Reason, &brief, &pending,
			&h.Status, &h.Owner, &h.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal(brief, &h.Brief)
	_ = json.Unmarshal(pending, &h.PendingAction)
	return &h, tx.Commit()
}

func (s pgHandoffs) Resolve(ctx context.Context, tenantID, id, status, owner string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE handoff_queue SET status=$2, owner=$3, resolved_at=now() WHERE id=$1`, id, status, owner)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgHandoffs) ListOpen(ctx context.Context, tenantID string) ([]model.HandoffItem, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, conversation_id, trigger_kind, reason, brief, pending_action, status, coalesce(owner,''), created_at
		 FROM handoff_queue WHERE status='OPEN' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.HandoffItem{}
	for rows.Next() {
		var h model.HandoffItem
		var brief, pending []byte
		if err := rows.Scan(&h.ID, &h.TenantID, &h.ConversationID, &h.Trigger, &h.Reason,
			&brief, &pending, &h.Status, &h.Owner, &h.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(brief, &h.Brief)
		_ = json.Unmarshal(pending, &h.PendingAction)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- outbox
type pgOutbox struct{ p *postgres }

func (s pgOutbox) Add(ctx context.Context, e *model.OutboxEvent) error {
	// NOTE: outbox rows are written by callers inside their own tenant tx in the
	// full implementation; this standalone path is used by the event forwarder tests.
	tx, err := s.p.tenantTx(ctx, e.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	payload, _ := json.Marshal(e.Payload)
	err = tx.QueryRowContext(ctx,
		`INSERT INTO outbox (tenant_id, aggregate, aggregate_id, event_type, payload)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		e.TenantID, e.Aggregate, e.AggregateID, e.EventType, payload).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (p *postgres) platformTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('app.platform_reader', 'on', true)"); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s pgOutbox) Unpublished(ctx context.Context, limit int) ([]model.OutboxEvent, error) {
	tx, err := s.p.platformTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, aggregate, aggregate_id, event_type, payload, created_at
		 FROM outbox WHERE published_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.OutboxEvent{}
	for rows.Next() {
		var e model.OutboxEvent
		var payload []byte
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Aggregate, &e.AggregateID, &e.EventType, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &e.Payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgOutbox) MarkPublished(ctx context.Context, ids []int64) error {
	tx, err := s.p.platformTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE outbox SET published_at=now() WHERE id = ANY($1)`, ids); err != nil {
		return err
	}
	return tx.Commit()
}

// ------------------------------------------------------------- attribution
type pgAttribution struct{ p *postgres }

func (s pgAttribution) Track(ctx context.Context, e *model.AttributionEvent) error {
	tx, err := s.p.tenantTx(ctx, e.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO attribution_events (tenant_id, kind, channel, campaign, client_id, request_id, value_minor)
		 VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,nullif($6,'')::uuid,$7)`,
		e.TenantID, e.Kind, e.Channel, e.Campaign, e.ClientID, e.RequestID, e.ValueMinor); err != nil {
		return err
	}
	return tx.Commit()
}
