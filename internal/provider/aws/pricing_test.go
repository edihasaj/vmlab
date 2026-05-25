package aws

import (
	"context"
	"os"
	"strings"
	"testing"
)

const samplePriceList = `{
  "PriceList": [
    "{\"terms\":{\"OnDemand\":{\"off1.JRTCKXETXF\":{\"priceDimensions\":{\"dim1.JRTCKXETXF.6YS6EN2CT7\":{\"pricePerUnit\":{\"USD\":\"0.0104000000\"}}}}}}}"
  ]
}`

func TestParseOnDemandUSDExtractsHourlyRate(t *testing.T) {
	rate, err := parseOnDemandUSD(samplePriceList)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.0104 {
		t.Errorf("expected 0.0104, got %v", rate)
	}
}

func TestParseOnDemandUSDEmptyPriceListErrors(t *testing.T) {
	_, err := parseOnDemandUSD(`{"PriceList":[]}`)
	if err == nil {
		t.Fatal("expected error on empty PriceList")
	}
}

// HourlyUSD short-circuits with rate=0 when instanceType isn't set so the
// budget gate skips cleanly instead of fanning out to a pointless API call.
func TestHourlyUSDNoInstanceTypeReturnsZero(t *testing.T) {
	// Reset cache so a prior test doesn't poison this one.
	priceCacheMu.Lock()
	priceCache = map[string]float64{}
	priceCacheMu.Unlock()

	_, rate, err := New().HourlyUSD(context.Background(), instance("nope", nil))
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0 {
		t.Errorf("expected rate=0 when instanceType is absent, got %v", rate)
	}
}

func TestHourlyUSDShellsCorrectFilters(t *testing.T) {
	dir := t.TempDir()
	args := stubAws(t, dir, `cat <<JSON
`+samplePriceList+`
JSON
exit 0`)
	withPath(t, dir)

	// Reset cache so the stub actually fires.
	priceCacheMu.Lock()
	priceCache = map[string]float64{}
	priceCacheMu.Unlock()

	inst := instance("demo", map[string]any{
		"region":       "eu-west-1",
		"instanceType": "t3.micro",
	})
	_, rate, err := New().HourlyUSD(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 0.0104 {
		t.Errorf("expected 0.0104, got %v", rate)
	}
	got := readArgs(t, args)
	for _, want := range []string{"pricing", "get-products", "instanceType,Value=t3.micro", "regionCode,Value=eu-west-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in argv: %s", want, got)
		}
	}
}

func readArgs(t *testing.T, path string) string {
	t.Helper()
	// Tests in this package already share read helpers via aws_test.go.
	// readLastArgs would suffice but we want a full slurp for multi-call
	// scripts.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
