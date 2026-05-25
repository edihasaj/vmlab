package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/edihasaj/vmlab/internal/provider"
)

// priceCache memoises lookups across all instances of the provider so a
// fleet of identical EC2 boxes hits the AWS pricing API only once. Keyed
// by `(instance-type, region, os, tenancy)`.
var (
	priceCache   = map[string]float64{}
	priceCacheMu sync.Mutex
)

// HourlyUSD implements provider.Priced. Queries the AWS Pricing API
// (`aws pricing get-products`) for the instance's on-demand rate.
//
// The Pricing API lives in us-east-1 only, so the call is hard-pinned to
// that region regardless of the instance's actual region. The instance's
// own region is passed via the `regionCode` filter so we get the right
// quote (us-east-2, eu-west-1, etc).
//
// Returns ("USD", 0, nil) when:
//   - ec2.instanceType isn't set on the instance (no SKU to look up).
//   - the API returns no matching PriceList (rare; usually means an
//     uncommon SKU + region combination).
//
// budget.EnforceBudget treats a 0 rate as "skip the check" — so a
// missing or unknown SKU still lets Up proceed; the operator declared
// the cap as documentation.
func (p *Provider) HourlyUSD(ctx context.Context, i provider.Instance) (string, float64, error) {
	itype := settingOr(i, "instanceType", "")
	if itype == "" {
		return "USD", 0, nil
	}
	region := i.SettingString("aws", "region")
	if region == "" {
		region = "us-east-1"
	}
	osKind := strings.ToLower(i.SettingString("ec2", "os"))
	if osKind == "" {
		osKind = "linux"
	}
	tenancy := i.SettingString("ec2", "tenancy")
	if tenancy == "" {
		tenancy = "Shared"
	}
	cacheKey := fmt.Sprintf("%s|%s|%s|%s", itype, region, osKind, tenancy)
	priceCacheMu.Lock()
	if rate, ok := priceCache[cacheKey]; ok {
		priceCacheMu.Unlock()
		return "USD", rate, nil
	}
	priceCacheMu.Unlock()

	args := []string{
		"pricing", "get-products",
		"--service-code", "AmazonEC2",
		"--filters",
		"Type=TERM_MATCH,Field=instanceType,Value=" + itype,
		"Type=TERM_MATCH,Field=regionCode,Value=" + region,
		"Type=TERM_MATCH,Field=operatingSystem,Value=" + osDisplay(osKind),
		"Type=TERM_MATCH,Field=tenancy,Value=" + tenancy,
		"Type=TERM_MATCH,Field=preInstalledSw,Value=NA",
		"Type=TERM_MATCH,Field=capacitystatus,Value=Used",
		"--region", "us-east-1",
		"--output", "json",
	}
	out, err := p.runRaw(ctx, args...)
	if err != nil {
		return "USD", 0, err
	}
	rate, err := parseOnDemandUSD(out)
	if err != nil {
		return "USD", 0, nil // unparseable → skip the check, not fail-closed
	}
	priceCacheMu.Lock()
	priceCache[cacheKey] = rate
	priceCacheMu.Unlock()
	return "USD", rate, nil
}

// runRaw fires aws with the caller's exact argv — no implicit --region or
// --profile prefix. The pricing API needs --region us-east-1 hard-coded,
// regardless of the instance's own region.
func (p *Provider) runRaw(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = os.Environ()
	if pr := os.Getenv("AWS_PROFILE"); pr != "" {
		// honoured via env, no flag needed
		_ = pr
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("aws pricing exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

// osDisplay maps the YAML-friendly os kind to the literal string AWS
// expects in the operatingSystem filter (capitalized human form).
func osDisplay(k string) string {
	switch strings.ToLower(k) {
	case "linux", "":
		return "Linux"
	case "windows":
		return "Windows"
	case "rhel":
		return "RHEL"
	case "suse":
		return "SUSE"
	}
	return strings.Title(k)
}

// parseOnDemandUSD walks the get-products response and extracts the first
// OnDemand price dimension's USD/hr value.
//
// Response shape (abridged):
//
//	{
//	  "PriceList": [
//	    "<stringified JSON>",
//	    ...
//	  ]
//	}
//
// Each PriceList[i] is a JSON STRING (double-encoded) that contains:
//
//	{
//	  "terms": {
//	    "OnDemand": {
//	      "<offer-id>": {
//	        "priceDimensions": {
//	          "<dim-id>": {
//	            "pricePerUnit": { "USD": "0.0104000000" }
//	          }
//	        }
//	      }
//	    }
//	  }
//	}
//
// We pick the first dimension we see; AWS only ships one OnDemand
// price per SKU + region combo in practice.
func parseOnDemandUSD(raw string) (float64, error) {
	var top struct {
		PriceList []string `json:"PriceList"`
	}
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return 0, err
	}
	if len(top.PriceList) == 0 {
		return 0, fmt.Errorf("aws pricing: empty PriceList")
	}
	var inner struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(top.PriceList[0]), &inner); err != nil {
		return 0, err
	}
	for _, offer := range inner.Terms.OnDemand {
		for _, dim := range offer.PriceDimensions {
			if v, ok := dim.PricePerUnit["USD"]; ok {
				rate, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return 0, err
				}
				return rate, nil
			}
		}
	}
	return 0, fmt.Errorf("aws pricing: no USD on-demand dimension found")
}
