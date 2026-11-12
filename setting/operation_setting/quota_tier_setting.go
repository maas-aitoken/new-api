package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// QuotaTierSetting runs subscription plans as quota tiers: users get the lowest plan, raising
// quota moves up one, and a used-up tier allows only "auto" requests. Off by default.
type QuotaTierSetting struct {
	Enabled bool `json:"enabled"`
}

var quotaTierSetting = QuotaTierSetting{}

func init() {
	config.GlobalConfig.Register("quota_tier_setting", &quotaTierSetting)
}

func IsQuotaTierEnabled() bool {
	return quotaTierSetting.Enabled
}

// SetQuotaTierEnabled is for tests.
func SetQuotaTierEnabled(enabled bool) {
	quotaTierSetting.Enabled = enabled
}
