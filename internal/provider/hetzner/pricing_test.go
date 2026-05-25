package hetzner

import (
	"context"
	"strings"
	"testing"
)

const sampleTypeList = `[
  {"name":"cax11","prices":[{"location":"hel1","price_hourly":{"gross":"0.0049"}},{"location":"nbg1","price_hourly":{"gross":"0.0050"}}]},
  {"name":"cax21","prices":[{"location":"hel1","price_hourly":{"gross":"0.0098"}}]}
]`

func TestHetznerHourlyEURPicksLocationMatch(t *testing.T) {
	rate, err := hetznerHourlyEUR(sampleTypeList, "cax11", "nbg1")
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.0050 {
		t.Errorf("expected 0.0050 EUR for cax11@nbg1, got %v", rate)
	}
}

func TestHetznerHourlyEURDefaultsToFirstLocationWhenUnset(t *testing.T) {
	rate, err := hetznerHourlyEUR(sampleTypeList, "cax11", "")
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.0049 {
		t.Errorf("expected 0.0049 EUR for first location, got %v", rate)
	}
}

func TestHetznerHourlyEURMissingTypeErrors(t *testing.T) {
	_, err := hetznerHourlyEUR(sampleTypeList, "cax-ghost", "hel1")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// HourlyUSD calls hcloud, parses, applies EUR->USD conversion. Stub hcloud
// returns the canned type list; assert the resulting USD rate matches the
// converted EUR price.
func TestHourlyUSDStubShellPath(t *testing.T) {
	dir := t.TempDir()
	stubHcloud(t, dir, `cat <<JSON
`+sampleTypeList+`
JSON
exit 0`)
	withPath(t, dir)

	priceCacheMu.Lock()
	priceCache = map[string]float64{}
	priceCacheMu.Unlock()

	// Pin conversion so the assertion is exact.
	getenv = func(string) string { return "1.10" }
	defer func() { getenv = func(s string) string { return "" } }()

	inst := instance("demo", map[string]any{
		"serverType": "cax11",
		"location":   "hel1",
	})
	_, rate, err := New().HourlyUSD(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	want := 0.0049 * 1.10
	if (rate-want) > 1e-9 || (want-rate) > 1e-9 {
		t.Errorf("expected %.10f, got %.10f", want, rate)
	}
	_ = strings.Contains
}
