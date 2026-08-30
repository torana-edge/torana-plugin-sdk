package plugin_sdk

import (
	"encoding/json"
	"fmt"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// Cache pricing
//
// The host holds prices and cache semantics, because those are operator
// configuration. The plugin holds policy, because that is what a plugin is for.
// This call is the seam: it reports what a cached prefix costs and how long it
// lives, and says nothing about what to do.
//
// Requires the env.host_call.torana_cache_pricing permission.

// CachePricing describes one provider/model's prompt-cache economics.
type CachePricing struct {
	Status string `json:"status"` // "ok" | "unavailable"
	Reason string `json:"reason,omitempty"`

	CacheReadUSDPerMTok  float64 `json:"cache_read_usd_per_mtok,omitempty"`
	CacheWriteUSDPerMTok float64 `json:"cache_write_usd_per_mtok,omitempty"`

	// WriteReadRatio is the write price over the read price.
	WriteReadRatio float64 `json:"write_read_ratio,omitempty"`

	// BreakEvenRefreshes is the largest number of refreshes that still costs
	// less than accepting the cache miss: floor(ratio - 1).
	//
	// The arithmetic is worth internalising, because it is the whole reason a
	// warming plugin cannot simply run forever. Holding an entry open costs one
	// cache read per refresh. Letting it lapse costs one cache write on the
	// next turn. So refreshing wins only while:
	//
	//	refreshes_spent < (write_rate / read_rate) - 1
	//
	// The prefix size cancels out of both sides — this is a pure price ratio,
	// independent of how large the conversation is. On a provider whose writes
	// cost 12.5x its reads, that is about 11 refreshes. Past that point,
	// refreshing has cost more than the miss it was avoiding, and it keeps
	// diverging rather than settling at break-even.
	BreakEvenRefreshes int `json:"break_even_refreshes,omitempty"`

	// RefreshOnRead reports whether reading an entry restarts its clock. When
	// false, nothing a plugin sends can keep an entry alive — the provider is
	// doing automatic prefix caching with a lifetime the caller does not own,
	// and a refresh request is pure cost.
	RefreshOnRead bool `json:"refresh_on_read"`

	ShortestTTLSeconds  int `json:"shortest_ttl_seconds,omitempty"`
	WarmIntervalSeconds int `json:"warm_interval_seconds,omitempty"`

	// Tiers are the cache lifetimes this provider sells, ascending by TTL.
	// Use these rather than hard-coding a provider's menu: the marker is
	// whatever the operator configured, and writing a different one changes the
	// prefix bytes and invalidates the entry you were trying to keep.
	Tiers []CacheTier `json:"tiers,omitempty"`
}

// CacheTier is one purchasable cache lifetime.
type CacheTier struct {
	TTLSeconds int `json:"ttl_seconds"`
	// WriteMultiplier is this tier's write cost relative to the model's base
	// input rate, so tiers can be compared without knowing the model.
	WriteMultiplier float64 `json:"write_multiplier,omitempty"`
	// Marker is the breakpoint value that selects this tier. Place it verbatim.
	Marker map[string]any `json:"marker,omitempty"`
}

// LongestTier returns the tier with the largest TTL, or false when the provider
// declares fewer than two — with nothing to choose between, there is no
// decision to make.
func (c CachePricing) LongestTier() (CacheTier, bool) {
	if len(c.Tiers) < 2 {
		return CacheTier{}, false
	}
	best := c.Tiers[0]
	for _, t := range c.Tiers[1:] {
		if t.TTLSeconds > best.TTLSeconds {
			best = t
		}
	}
	return best, true
}

// Available reports whether the host could answer. When false, Reason says why,
// and a plugin that would spend money must decline rather than assume a
// default: unknown pricing is exactly the case where guessing is most expensive.
func (c CachePricing) Available() bool { return c.Status == "ok" }

// Warmable reports whether refreshing this provider's cache can work at all —
// it must have a lifetime that reads restart, and a tier to race against.
func (c CachePricing) Warmable() bool {
	return c.Available() && c.RefreshOnRead && c.ShortestTTLSeconds > 0
}

// GetCachePricing asks the host about one provider/model pair.
//
// Refusal classification:
//
//   - NOT_CONFIGURED, UNAVAILABLE — expected advisory refusals: degrade to
//     CachePricing{Status: "unavailable"} with the reason token, exactly as if
//     the host had answered "no pricing right now". A warming plugin must
//     decline rather than assume a default.
//   - PERMISSION_DENIED — NOT an advisory gap. Torana approvals are
//     all-or-nothing: an enabled plugin has every declared permission, so a
//     permission refusal means the plugin called a capability it did not
//     declare (an author bug) or the host violated the approval invariant.
//     Surfaced as an error either way.
//   - INVALID_ARGUMENT, NOT_FOUND — plugin bugs (malformed query, unknown
//     command); surfaced as errors so the author fixes the caller instead of
//     debugging a silent "unavailable".
//   - INTERNAL and any unknown code — host defects; surfaced as errors.
//   - An empty success value — torana_cache_pricing is a QUERY: the host must
//     return either a pricing envelope or a refusal. An empty value is a
//     protocol/host defect, not "no pricing".
func GetCachePricing(provider, model string) (CachePricing, error) {
	payload, err := json.Marshal(map[string]string{"provider": provider, "model": model})
	if err != nil {
		return CachePricing{}, err
	}
	res, herr, err := HostCallExtension("torana_cache_pricing", payload)
	if err != nil {
		return CachePricing{}, err
	}
	if herr != nil {
		// Expected advisory refusals degrade; permission/caller/host defects
		// surface.
		switch herr.Code {
		case pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
			pbv1.ErrorCode_ERROR_CODE_UNAVAILABLE:
			return CachePricing{Status: "unavailable", Reason: hostErrorReason(herr)}, nil
		default:
			return CachePricing{}, classifiedRefusal(herr)
		}
	}
	if len(res) == 0 {
		return CachePricing{}, fmt.Errorf("torana: host returned no cache pricing result")
	}
	var out CachePricing
	if err := json.Unmarshal(res, &out); err != nil {
		return CachePricing{}, fmt.Errorf("torana: decode cache pricing: %w", err)
	}
	return out, nil
}
