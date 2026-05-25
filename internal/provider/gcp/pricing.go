package gcp

import (
	"context"

	"github.com/edihasaj/vmlab/internal/provider"
)

// HourlyUSD implements provider.Priced. Unlike AWS (`aws pricing
// get-products`) and Azure (public Retail Prices API), GCP's Cloud Billing
// Catalog API needs an explicit API key + enabled service, and SKU naming
// is fuzzy enough that a programmatic match is error-prone. We therefore
// ship a minimal-but-honest impl:
//
//  1. If the operator sets `gcp.hourlyUSD` on the instance, use that.
//     Best for known-good machine types: easy override, no surprises.
//  2. Otherwise, return ("USD", 0, nil). The budget gate treats this as
//     "no provider-known rate" and lets Up proceed — the operator's
//     budget cap acts as documentation only.
//
// Live Cloud Billing Catalog integration is a worthwhile follow-up, but
// gating Up on a half-working SKU match is worse than a clear opt-in
// override. Document the override pattern and move on.
func (p *Provider) HourlyUSD(_ context.Context, i provider.Instance) (string, float64, error) {
	if v, ok := i.Setting("gcp", "hourlyUSD").(float64); ok && v > 0 {
		return "USD", v, nil
	}
	return "USD", 0, nil
}
