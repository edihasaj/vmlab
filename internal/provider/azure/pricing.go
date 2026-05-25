package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/edihasaj/vmlab/internal/provider"
)

// priceCache memoises Retail Prices API lookups across the process so a
// fleet of identical VMs hits the public endpoint at most once. Keyed by
// `(armSkuName, armRegionName)`.
var (
	priceCache   = map[string]float64{}
	priceCacheMu sync.Mutex
)

// retailPricesEndpoint is the public, unauthenticated Azure Retail Prices
// API. Documented at:
// https://learn.microsoft.com/en-us/rest/api/cost-management/retail-prices
//
// Filter syntax is OData $filter. We pin currencyCode=USD,
// priceType=Consumption (excluding reserved-instance and spot rows), and
// match the VM's SKU + region.
var retailPricesEndpoint = "https://prices.azure.com/api/retail/prices"

// retailPricesEndpointSwap replaces the endpoint URL for the duration of a
// test and returns a restorer. Test-only knob — production callers never
// touch this.
func retailPricesEndpointSwap(url string) func() {
	prev := retailPricesEndpoint
	retailPricesEndpoint = url
	return func() { retailPricesEndpoint = prev }
}

// pricingHTTPClient is exposed so tests can swap in a fake transport
// without monkey-patching net/http globals.
var pricingHTTPClient = &http.Client{Timeout: 10 * time.Second}

// HourlyUSD implements provider.Priced. Queries the Azure Retail Prices
// API for the VM's consumption-tier on-demand rate. Public endpoint, no
// auth required — handy for budget pre-flight before any `az` call.
//
// Returns ("USD", 0, nil) when:
//   - azure.size or azure.location aren't set on the instance.
//   - the API returns no matching Items.
//
// budget.EnforceBudget treats a 0 rate as "skip the check."
func (p *Provider) HourlyUSD(ctx context.Context, i provider.Instance) (string, float64, error) {
	size := settingOr(i, "size", "")
	region := i.SettingString("azure", "location")
	if size == "" || region == "" {
		return "USD", 0, nil
	}
	cacheKey := size + "|" + region
	priceCacheMu.Lock()
	if rate, ok := priceCache[cacheKey]; ok {
		priceCacheMu.Unlock()
		return "USD", rate, nil
	}
	priceCacheMu.Unlock()

	filter := fmt.Sprintf(
		"armSkuName eq '%s' and armRegionName eq '%s' and priceType eq 'Consumption' and currencyCode eq 'USD'",
		size, region,
	)
	q := url.Values{}
	q.Set("$filter", filter)
	q.Set("api-version", "2023-01-01-preview")
	endpoint := retailPricesEndpoint + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "USD", 0, err
	}
	resp, err := pricingHTTPClient.Do(req)
	if err != nil {
		return "USD", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "USD", 0, fmt.Errorf("azure retail prices: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "USD", 0, err
	}
	rate, err := parseAzureLowestUnitPrice(body)
	if err != nil {
		return "USD", 0, nil
	}
	priceCacheMu.Lock()
	priceCache[cacheKey] = rate
	priceCacheMu.Unlock()
	return "USD", rate, nil
}

// parseAzureLowestUnitPrice walks the Items array and picks the lowest
// non-zero unit price. Azure ships separate SKUs per OS (Linux vs Windows
// licensed), so the safe default for "what would my VM cost" is the Linux
// row, which is also the lowest. Callers that want a specific OS can
// pre-filter via SKU naming, but for the budget check the floor is
// honest enough.
func parseAzureLowestUnitPrice(body []byte) (float64, error) {
	var resp struct {
		Items []struct {
			UnitPrice     float64 `json:"unitPrice"`
			ProductName   string  `json:"productName"`
			SkuName       string  `json:"skuName"`
			ArmSkuName    string  `json:"armSkuName"`
			MeterName     string  `json:"meterName"`
			UnitOfMeasure string  `json:"unitOfMeasure"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	var lowest float64 = -1
	for _, it := range resp.Items {
		// Skip "Low Priority" (spot) variants — they're not what the
		// budget gate is meant to cap against.
		if strings.Contains(strings.ToLower(it.MeterName), "low priority") ||
			strings.Contains(strings.ToLower(it.SkuName), "spot") {
			continue
		}
		if it.UnitPrice <= 0 {
			continue
		}
		if lowest < 0 || it.UnitPrice < lowest {
			lowest = it.UnitPrice
		}
	}
	if lowest < 0 {
		return 0, fmt.Errorf("azure retail prices: no matching items")
	}
	return lowest, nil
}
