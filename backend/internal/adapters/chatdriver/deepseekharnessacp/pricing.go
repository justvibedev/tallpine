package deepseekharnessacp

import "time"

// price is USD per one million tokens.
type price struct{ cacheHit, cacheMiss, output float64 }

// offPeakPrices is the DeepSeek official API price list as of 2026-10-27, keyed by the model name dsh logs on each request. Peak is
// double (peakMultiplier). A model absent from the table has no known price, so
// its tokens are shown with no cost rather than a guessed one (deepseek-v4-pro
// is deliberately not listed).
var offPeakPrices = map[string]price{
	"deepseek-flash": {cacheHit: 0.003, cacheMiss: 0.15, output: 0.60},
}

const peakMultiplier = 2

// isPeak reports whether t falls in DeepSeek's peak window: 01:00–04:00 and
// 06:00–10:00 UTC, Monday to Friday (start inclusive, end exclusive). Public
// holidays are ignored: the provider's calendar for them is not modelled here.
func isPeak(t time.Time) bool {
	t = t.UTC()
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	h := t.Hour()
	return (h >= 1 && h < 4) || (h >= 6 && h < 10)
}

// requestCost prices one request by its own timestamp. ok is false for a model
// without a known price.
func requestCost(model string, at time.Time, miss, hit, output int64) (usd float64, ok bool) {
	p, ok := offPeakPrices[model]
	if !ok {
		return 0, false
	}
	if isPeak(at) {
		p = price{p.cacheHit * peakMultiplier, p.cacheMiss * peakMultiplier, p.output * peakMultiplier}
	}
	return (float64(hit)*p.cacheHit + float64(miss)*p.cacheMiss + float64(output)*p.output) / 1e6, true
}
