package azure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAzureLowestUnitPriceSkipsSpot(t *testing.T) {
	body := []byte(`{
"Items": [
  {"unitPrice": 0.0416, "armSkuName": "Standard_D2s_v3", "meterName": "D2s v3", "skuName": "D2s v3", "productName": "Virtual Machines DSv3 Series", "unitOfMeasure": "1 Hour"},
  {"unitPrice": 0.0083, "armSkuName": "Standard_D2s_v3", "meterName": "D2s v3 Low Priority", "skuName": "D2s v3 Spot", "productName": "Virtual Machines DSv3 Series", "unitOfMeasure": "1 Hour"}
]
}`)
	rate, err := parseAzureLowestUnitPrice(body)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.0416 {
		t.Errorf("expected the consumption price 0.0416, not the spot 0.0083; got %v", rate)
	}
}

func TestHourlyUSDFetchesAzureRetailPrices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sanity-check we sent the right $filter — caller's SKU + region.
		got := r.URL.Query().Get("$filter")
		if !contains(got, "armSkuName eq 'Standard_B1s'") || !contains(got, "armRegionName eq 'eastus'") {
			t.Errorf("unexpected $filter: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Items":[{"unitPrice":0.0124,"armSkuName":"Standard_B1s","meterName":"B1s","skuName":"B1s","unitOfMeasure":"1 Hour"}]}`))
	}))
	defer srv.Close()

	// Swap in the test endpoint + a client that talks to it.
	priceCacheMu.Lock()
	priceCache = map[string]float64{}
	priceCacheMu.Unlock()

	prev := retailPricesEndpointSwap(srv.URL + "/api/retail/prices")
	defer prev()

	inst := instance("demo", map[string]any{
		"size":     "Standard_B1s",
		"location": "eastus",
	})
	_, rate, err := New().HourlyUSD(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.0124 {
		t.Errorf("expected 0.0124, got %v", rate)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
