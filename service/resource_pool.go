package service

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
	"golang.org/x/sync/singleflight"
)

// resourcePoolFetchTimeout bounds one upstream admin request.
const resourcePoolFetchTimeout = 10 * time.Second

// resourcePoolAPIReply is the upstream payload shape. Verticals in this fleet
// answer either {accounts:[...]} or {data:[...]}.
type resourcePoolAPIReply struct {
	Accounts []*model.ResourcePoolAccount `json:"accounts"`
	Data     []*model.ResourcePoolAccount `json:"data"`
}

// ResourcePoolResult bundles the flattened account list with per-source status.
// A cached result is shared between requests and must be treated as read-only.
type ResourcePoolResult struct {
	Accounts []*model.ResourcePoolAccount      `json:"accounts"`
	Sources  []*model.ResourcePoolSourceStatus `json:"sources"`
	// RefreshedAt is the unix time the upstream sources were polled.
	RefreshedAt int64 `json:"refreshed_at"`
}

const (
	// resourcePoolFreshTTL serves a cached poll without touching the upstream.
	resourcePoolFreshTTL = 30 * time.Second
	// resourcePoolStaleTTL serves an older poll immediately and refreshes it
	// in the background; past this age the caller waits for a new poll.
	resourcePoolStaleTTL = 10 * time.Minute
)

type resourcePoolCacheEntry struct {
	result    *ResourcePoolResult
	fetchedAt time.Time
}

// resourcePoolCache keeps the last poll of every provider. codex2api answers
// in 1.5–2s, so polling on every console request makes each provider switch
// and refresh wait on the upstream.
var resourcePoolCache = struct {
	sync.Mutex
	entries map[string]resourcePoolCacheEntry
	// invalidatedAt drops polls that started before a source config change.
	invalidatedAt map[string]time.Time
	flight        singleflight.Group
}{
	entries:       make(map[string]resourcePoolCacheEntry),
	invalidatedAt: make(map[string]time.Time),
}

// GetResourcePoolUsage returns the provider's pool usage from the cache when it
// is fresh, serves a stale copy while refreshing it in the background, and
// polls the upstream synchronously when forced or when nothing usable is cached.
func GetResourcePoolUsage(provider string, force bool) (*ResourcePoolResult, error) {
	resourcePoolCache.Lock()
	entry, ok := resourcePoolCache.entries[provider]
	resourcePoolCache.Unlock()

	if ok && !force {
		age := time.Since(entry.fetchedAt)
		if age < resourcePoolFreshTTL {
			return entry.result, nil
		}
		if age < resourcePoolStaleTTL {
			gopool.Go(func() {
				if _, err := refreshResourcePoolUsage(provider); err != nil {
					common.SysError(common.LogText("failed to refresh resource pool usage: %s", err.Error()))
				}
			})
			return entry.result, nil
		}
	}
	return refreshResourcePoolUsage(provider)
}

// refreshResourcePoolUsage polls the upstream once per provider no matter how
// many callers ask at the same time, then stores the result.
func refreshResourcePoolUsage(provider string) (*ResourcePoolResult, error) {
	value, err, _ := resourcePoolCache.flight.Do(provider, func() (any, error) {
		startedAt := time.Now()
		result, err := FetchResourcePoolUsage(provider)
		if err != nil {
			return nil, err
		}
		resourcePoolCache.Lock()
		if startedAt.After(resourcePoolCache.invalidatedAt[provider]) {
			resourcePoolCache.entries[provider] = resourcePoolCacheEntry{result: result, fetchedAt: startedAt}
		}
		resourcePoolCache.Unlock()
		return result, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*ResourcePoolResult), nil
}

// InvalidateResourcePoolUsage drops cached polls after a source config change.
// An empty provider drops every provider.
func InvalidateResourcePoolUsage(provider string) {
	now := time.Now()
	resourcePoolCache.Lock()
	defer resourcePoolCache.Unlock()
	if provider != "" {
		delete(resourcePoolCache.entries, provider)
		resourcePoolCache.invalidatedAt[provider] = now
		return
	}
	for key := range resourcePoolCache.entries {
		delete(resourcePoolCache.entries, key)
	}
	for _, key := range model.ResourcePoolProviders {
		resourcePoolCache.invalidatedAt[key] = now
	}
}

// FetchResourcePoolUsage polls every enabled source of one provider in
// parallel and flattens the returned accounts. Disabled sources contribute a
// "disabled" status row only, so the console can still list them.
func FetchResourcePoolUsage(provider string) (*ResourcePoolResult, error) {
	sources, err := model.GetResourcePoolSources(provider)
	if err != nil {
		return nil, err
	}

	result := &ResourcePoolResult{
		Accounts: make([]*model.ResourcePoolAccount, 0),
		Sources:  make([]*model.ResourcePoolSourceStatus, 0, len(sources)),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, source := range sources {
		if !source.Enabled {
			result.Sources = append(result.Sources, &model.ResourcePoolSourceStatus{
				SourceId:       source.Id,
				SourceName:     source.Name,
				SourceEndpoint: source.Endpoint,
				Status:         "disabled",
			})
			continue
		}

		wg.Add(1)
		go func(source *model.ResourcePoolSource) {
			defer wg.Done()
			accounts, status := fetchResourcePoolSource(source)
			mu.Lock()
			result.Accounts = append(result.Accounts, accounts...)
			result.Sources = append(result.Sources, status)
			mu.Unlock()
		}(source)
	}
	wg.Wait()

	// Goroutines finish in arbitrary order; keep rows stable across polls.
	slices.SortStableFunc(result.Sources, func(a, b *model.ResourcePoolSourceStatus) int {
		return cmp.Compare(a.SourceId, b.SourceId)
	})
	slices.SortStableFunc(result.Accounts, func(a, b *model.ResourcePoolAccount) int {
		return cmp.Compare(a.SourceId, b.SourceId)
	})
	result.RefreshedAt = common.GetTimestamp()
	return result, nil
}

// fetchResourcePoolSource polls one upstream endpoint and refreshes its stored
// snapshot on success.
func fetchResourcePoolSource(source *model.ResourcePoolSource) ([]*model.ResourcePoolAccount, *model.ResourcePoolSourceStatus) {
	status := &model.ResourcePoolSourceStatus{
		SourceId:       source.Id,
		SourceName:     source.Name,
		SourceEndpoint: source.Endpoint,
		Status:         "failed",
	}

	if strings.TrimSpace(source.Endpoint) == "" {
		status.Error = "接口地址为空"
		return nil, status
	}

	ctx, cancel := context.WithTimeout(context.Background(), resourcePoolFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.Endpoint, nil)
	if err != nil {
		status.Error = err.Error()
		return nil, status
	}
	// The upstream admin APIs disagree on the auth header convention;
	// sending all three keeps one importer compatible with every vertical:
	//   - X-Admin-Password / X-Api-Key: tokenmanagement (Claude pool)
	//   - X-Admin-Key: codex2api (Grok pool)
	if source.AdminPassword != "" {
		req.Header.Set("X-Admin-Password", source.AdminPassword)
		req.Header.Set("X-Api-Key", source.AdminPassword)
		req.Header.Set("X-Admin-Key", source.AdminPassword)
	}

	resp, err := resourcePoolHTTPClient().Do(req)
	if err != nil {
		status.Error = err.Error()
		return nil, status
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		status.Error = err.Error()
		return nil, status
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		status.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateBody(string(body)))
		return nil, status
	}

	var reply resourcePoolAPIReply
	if err := common.Unmarshal(body, &reply); err != nil {
		status.Error = err.Error()
		return nil, status
	}
	accounts := reply.Accounts
	if accounts == nil {
		accounts = reply.Data
	}
	if accounts == nil {
		status.Error = "接口返回格式不正确"
		return nil, status
	}

	valid := make([]*model.ResourcePoolAccount, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		account.SourceId = source.Id
		account.SourceName = source.Name
		account.SourceEndpoint = source.Endpoint
		// The Kiro pool (tokenmanagement) reports the group per account
		// (groups / sourceChannel) and one source mixes several groups, so
		// the source's configured group is only a fallback.
		if account.SourceGroup == "" {
			account.SourceGroup = resourcePoolAccountGroup(account, source.GroupName)
		}
		// Normalise the three usage fields the upstream verticals disagree on.
		if account.UsagePercentage == 0 && account.UsagePercent > 0 {
			account.UsagePercentage = account.UsagePercent * 100
		}
		if account.UsagePercentage == 0 && account.UsagePercent7d != nil {
			// codex2api (Grok pool): the 7d window percentage is the primary
			// water-level metric.
			account.UsagePercentage = *account.UsagePercent7d
		}
		if account.UsagePercentage == 0 && account.WeeklyPercent != nil {
			// Newer codex2api builds report the weekly billing percentage in
			// grok_billing.weekly_percent instead of usage_percent_7d.
			account.UsagePercentage = *account.WeeklyPercent
		}
		if account.UsagePercentage == 0 && account.UsageLimit > 0 {
			account.UsagePercentage = account.UsageCurrent / account.UsageLimit * 100
		}
		// codex2api has no single "current usage" counter; surface the 7d
		// token total so the console can rank accounts by load.
		if account.UsageCurrent == 0 && account.Tokens7d > 0 {
			account.UsageCurrent = float64(account.Tokens7d)
		}
		// codex2api marks account health via status strings rather than an
		// enabled flag on some rows; treat empty status as enabled=true only
		// when the row came from the claude pool, which always sets Enabled.
		valid = append(valid, account)
	}

	if snapshot, err := common.Marshal(valid); err == nil {
		if err := model.UpdateResourcePoolSourceSnapshot(source.Id, string(snapshot), common.GetTimestamp()); err != nil {
			common.SysError(common.LogText("failed to save resource pool snapshot: %s", err.Error()))
		}
	}

	status.Status = "success"
	status.Count = len(valid)
	status.Error = ""
	return valid, status
}

// resourcePoolAccountGroup picks the account's own group, falling back to the
// group configured on its source.
func resourcePoolAccountGroup(account *model.ResourcePoolAccount, fallback string) string {
	for _, group := range account.Groups {
		if group = strings.TrimSpace(group); group != "" {
			return group
		}
	}
	if channel := strings.TrimSpace(account.SourceChannel); channel != "" {
		return channel
	}
	return fallback
}

// resourcePoolHTTPClient prefers the shared upstream client but stays usable
// before InitHttpClient has run (for example in unit tests).
func resourcePoolHTTPClient() *http.Client {
	if client := GetHttpClient(); client != nil {
		return client
	}
	return http.DefaultClient
}

// truncateBody keeps upstream error bodies readable in the console.
func truncateBody(body string) string {
	const limit = 200
	body = strings.TrimSpace(body)
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "..."
}
