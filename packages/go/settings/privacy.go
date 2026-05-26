package settings

import (
	"encoding/json"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Privacy setting keys — issue #225. These back the Settings → Privacy
// admin form (cookie policy URL + text, retention windows for the
// audit / sessions / login-attempts streams, and the GDPR self-service
// kill switch). Mirrors the WordPress "privacy settings" surface, but
// surfaces retention as first-class typed values instead of free-form
// help text.
const (
	// PrivacyCookiePolicyURL is the absolute URL of the site's cookie
	// policy. Themes link to it from the consent banner and footer.
	PrivacyCookiePolicyURL = "core.privacy.cookie_policy_url"
	// PrivacyCookiePolicyText is the human-readable text shown in the
	// cookie consent banner. Plain text; themes wrap it.
	PrivacyCookiePolicyText = "core.privacy.cookie_policy_text"

	// Retention windows are expressed in days. A zero value means
	// "retain forever"; the retention job treats anything > 0 as the
	// max-age cutoff.
	PrivacyRetentionAuditDays         = "core.privacy.retention.audit_days"
	PrivacyRetentionSessionsDays      = "core.privacy.retention.sessions_days"
	PrivacyRetentionLoginAttemptsDays = "core.privacy.retention.login_attempts_days"

	// PrivacyAllowGDPRSelfService gates the public
	// /api/v1/account/data/export endpoint. When false, the endpoint
	// returns 403 and the admin UI hides the user-facing data export
	// affordance.
	PrivacyAllowGDPRSelfService = "core.privacy.allow_gdpr_self_service"
)

// PrivacySettings returns the privacy-group settings registered onto
// the core registry. Kept separate from [CoreSettings] so the group
// can grow without touching the existing core seed list.
func PrivacySettings() []Setting {
	return []Setting{
		{
			Key:                PrivacyCookiePolicyURL,
			Description:        "Absolute URL of the site's cookie policy. Themes link to it from the consent banner and footer.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","maxLength":2048}`),
			Default:            "",
			Autoload:           true,
			Group:              GroupPrivacy,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                PrivacyCookiePolicyText,
			Description:        "Human-readable text shown in the cookie consent banner.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","maxLength":4096}`),
			Default:            "This site uses cookies to keep you signed in and remember your preferences.",
			Autoload:           true,
			Group:              GroupPrivacy,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                PrivacyRetentionAuditDays,
			Description:        "Number of days to retain audit-log entries. 0 means retain indefinitely.",
			Type:               SettingTypeInt,
			Schema:             json.RawMessage(`{"type":"integer","minimum":0,"maximum":3650}`),
			Default:            float64(365),
			Autoload:           true,
			Group:              GroupPrivacy,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                PrivacyRetentionSessionsDays,
			Description:        "Number of days to retain expired session records. 0 means retain indefinitely.",
			Type:               SettingTypeInt,
			Schema:             json.RawMessage(`{"type":"integer","minimum":0,"maximum":3650}`),
			Default:            float64(30),
			Autoload:           true,
			Group:              GroupPrivacy,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                PrivacyRetentionLoginAttemptsDays,
			Description:        "Number of days to retain failed-login attempt records. 0 means retain indefinitely.",
			Type:               SettingTypeInt,
			Schema:             json.RawMessage(`{"type":"integer","minimum":0,"maximum":3650}`),
			Default:            float64(90),
			Autoload:           true,
			Group:              GroupPrivacy,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                PrivacyAllowGDPRSelfService,
			Description:        "Whether users may export their personal data via /api/v1/account/data/export. When false, the endpoint returns 403.",
			Type:               SettingTypeBool,
			Schema:             json.RawMessage(`{"type":"boolean"}`),
			Default:            true,
			Autoload:           true,
			Group:              GroupPrivacy,
			RequiresCapability: policy.CapManageOptions,
		},
	}
}

// RegisterPrivacy adds the privacy-group settings to reg. Call after
// [RegisterCore].
func RegisterPrivacy(reg *Registry) error {
	for _, s := range PrivacySettings() {
		if err := reg.Register(s); err != nil {
			return err
		}
	}
	return nil
}
