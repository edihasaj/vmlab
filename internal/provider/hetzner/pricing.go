package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/edihasaj/vmlab/internal/provider"
)

var (
	priceCache   = map[string]float64{}
	priceCacheMu sync.Mutex
)

// HourlyUSD implements provider.Priced. Hetzner publishes hourly prices
// per (server-type, location) directly in `hcloud server-type list -o json`,
// quoted in EUR. We convert to USD via a coarse conversion rate so the
// budget gate can speak a single currency to the operator.
//
// Conversion: HETZNER_EUR_USD env var overrides; otherwise we use a
// reasonable static fallback (1 EUR ≈ 1.07 USD as of 2026-05). The number
// doesn't have to be precise — it's a budget cap, not an invoice.
//
// Returns ("USD", 0, nil) when:
//   - hetzner.serverType isn't set on the instance.
//   - the type isn't listed in the API response (very rare).
//   - the type has no price entry for the instance's location.
func (p *Provider) HourlyUSD(ctx context.Context, i provider.Instance) (string, float64, error) {
	stype := settingOr(i, "serverType", "")
	if stype == "" {
		return "USD", 0, nil
	}
	location := i.SettingString("hetzner", "location")
	cacheKey := stype + "|" + location
	priceCacheMu.Lock()
	if rate, ok := priceCache[cacheKey]; ok {
		priceCacheMu.Unlock()
		return "USD", rate, nil
	}
	priceCacheMu.Unlock()

	out, err := p.run(ctx, i, "server-type", "list", "-o", "json")
	if err != nil {
		return "USD", 0, err
	}
	rateEUR, err := hetznerHourlyEUR(out, stype, location)
	if err != nil || rateEUR <= 0 {
		return "USD", 0, nil
	}
	rate := rateEUR * eurUSDRate()
	priceCacheMu.Lock()
	priceCache[cacheKey] = rate
	priceCacheMu.Unlock()
	return "USD", rate, nil
}

// hetznerHourlyEUR pulls the matching (type, location) entry's gross
// hourly price (EUR) out of the API response. If location is empty, we
// take the first location's price for the type as a reasonable default.
func hetznerHourlyEUR(raw, stype, location string) (float64, error) {
	var list []struct {
		Name   string `json:"name"`
		Prices []struct {
			Location    string `json:"location"`
			PriceHourly struct {
				Gross string `json:"gross"`
			} `json:"price_hourly"`
		} `json:"prices"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &list); err != nil {
		return 0, err
	}
	for _, t := range list {
		if t.Name != stype {
			continue
		}
		for _, pr := range t.Prices {
			if location == "" || pr.Location == location {
				return strconv.ParseFloat(pr.PriceHourly.Gross, 64)
			}
		}
	}
	return 0, fmt.Errorf("hetzner pricing: no entry for %s @ %s", stype, location)
}

// eurUSDRate honours HETZNER_EUR_USD env var (e.g. "1.08") for ops that
// want pinpoint accuracy; otherwise a coarse default. Static is fine for
// a budget cap — within 5% of reality is good enough to gate.
func eurUSDRate() float64 {
	if s := getenv("HETZNER_EUR_USD"); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
			return f
		}
	}
	return 1.07
}

// getenv is a thin os.Getenv indirection so tests can swap it without
// touching real process env.
var getenv = func(k string) string { return os.Getenv(k) }
