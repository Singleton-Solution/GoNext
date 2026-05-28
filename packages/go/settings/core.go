package settings

import (
	"encoding/json"

	"github.com/Singleton-Solution/GoNext/packages/go/policy"
)

// Core setting group names. These are the keys the admin UI uses to
// page through settings (one form page per group). They mirror the
// WP sidebar layout documented in docs/05-admin-api.md §2.6.
//
// Plugins are encouraged to reuse these group names where their setting
// fits naturally ("a custom favicon" lives under "general", not
// "myplugin"). Plugins that introduce a new top-level group should
// declare it in their manifest.
const (
	GroupGeneral    = "general"
	GroupReading    = "reading"
	GroupWriting    = "writing"
	GroupDiscussion = "discussion"
	GroupMedia      = "media"
	GroupPermalinks = "permalinks"
	GroupPrivacy    = "privacy"
)

// CoreSettings returns the list of pre-declared core Settings that
// mirror the rows seeded by migration 000008. The list is the single
// source of truth shared by:
//
//   - RegisterCore(r), which seeds a runtime registry.
//   - The admin-API test suite, which asserts these keys are
//     registered.
//   - Future docs-generation tooling that needs a programmatic list of
//     "what settings does core ship".
//
// Schemas are JSON Schema 2020-12. Keeping them inline (rather than
// loading from .json files at init) keeps the binary self-contained;
// migration 000008 ships the matching values.
func CoreSettings() []Setting {
	return []Setting{
		{
			Key:                "core.site.name",
			Description:        "The public name of this site, shown in the browser tab and page titles.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","minLength":1,"maxLength":80}`),
			Default:            "My GoNext Site",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.site.tagline",
			Description:        "A short tagline shown alongside the site name in some themes.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","maxLength":160}`),
			Default:            "Just another GoNext site",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.site.url",
			Description:        "The canonical absolute URL of this site. Used for redirects, canonical tags, and link generation.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","format":"uri","pattern":"^https?://"}`),
			Default:            "http://localhost:8080",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.site.default_role",
			Description:        "The role assigned to new user registrations.",
			Type:               SettingTypeEnum,
			Schema:             json.RawMessage(`{"type":"string","enum":["subscriber","contributor","author","editor","admin"]}`),
			Default:            "subscriber",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.timezone",
			Description:        "Default IANA timezone for dates shown in the admin (e.g. UTC, America/New_York).",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","minLength":3,"maxLength":64}`),
			Default:            "UTC",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.locale",
			Description:        "Default site locale (BCP 47 tag, e.g. en-US, fr-FR). Themes and plugins fall back to this when no per-user override is set.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","pattern":"^[a-z]{2,3}(-[A-Z][a-zA-Z]{1,7})?$"}`),
			Default:            "en-US",
			Autoload:           true,
			Group:              GroupGeneral,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.permalinks.format",
			Description:        "URL structure for posts. Common values: /%postname%/, /%year%/%monthnum%/%postname%/.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","minLength":1,"maxLength":200,"pattern":"^/"}`),
			Default:            "/%postname%/",
			Autoload:           true,
			Group:              GroupPermalinks,
			RequiresCapability: policy.CapManageOptions,
		},

		// Reading group — issue #525. Mirrors the form rendered by
		// apps/admin/.../settings/reading/ReadingForm.tsx. Keys here are
		// the canonical names the admin client sends; renaming requires
		// a coordinated change in both places.
		{
			Key:                "core.reading.homepage_type",
			Description:        "What visitors see at the site root: the latest-posts index or a designated static page.",
			Type:               SettingTypeEnum,
			Schema:             json.RawMessage(`{"type":"string","enum":["latest_posts","static_page"]}`),
			Default:            "latest_posts",
			Autoload:           true,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.reading.homepage_page_id",
			Description:        "Slug or ID of the static page used as the homepage when homepage_type=static_page. Empty otherwise.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","maxLength":200}`),
			Default:            "",
			Autoload:           true,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.reading.posts_per_page",
			Description:        "Number of posts shown on the blog index and archive pages.",
			Type:               SettingTypeInt,
			Schema:             json.RawMessage(`{"type":"integer","minimum":1,"maximum":100}`),
			Default:            float64(10),
			Autoload:           true,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.reading.show_summary",
			Description:        "When true, archive pages show post excerpts instead of the full body.",
			Type:               SettingTypeBool,
			Schema:             json.RawMessage(`{"type":"boolean"}`),
			Default:            true,
			Autoload:           true,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.reading.rss_items",
			Description:        "Number of entries served at /feed/.",
			Type:               SettingTypeInt,
			Schema:             json.RawMessage(`{"type":"integer","minimum":1,"maximum":100}`),
			Default:            float64(10),
			Autoload:           true,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.reading.rss_full_text",
			Description:        "When true, RSS items include the full post body; when false, only excerpts.",
			Type:               SettingTypeBool,
			Schema:             json.RawMessage(`{"type":"boolean"}`),
			Default:            false,
			Autoload:           true,
			Group:              GroupReading,
			RequiresCapability: policy.CapManageOptions,
		},

		// Writing group — issue #525. Mirrors
		// apps/admin/.../settings/writing/WritingForm.tsx. The enum lists
		// here are seed defaults; richer category/format pickers arrive
		// with Posts (#31) and Taxonomies (#32).
		{
			Key:                "core.writing.default_category",
			Description:        "Default taxonomy term applied to new posts that do not explicitly set one.",
			Type:               SettingTypeEnum,
			Schema:             json.RawMessage(`{"type":"string","enum":["uncategorized","news","blog","updates"]}`),
			Default:            "uncategorized",
			Autoload:           true,
			Group:              GroupWriting,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.writing.default_format",
			Description:        "Default post-format hint themes use to pick a template for new posts.",
			Type:               SettingTypeEnum,
			Schema:             json.RawMessage(`{"type":"string","enum":["standard","aside","gallery","link","quote"]}`),
			Default:            "standard",
			Autoload:           true,
			Group:              GroupWriting,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.writing.default_editor",
			Description:        "Which editor opens when an author clicks New post: the block editor or the classic editor.",
			Type:               SettingTypeEnum,
			Schema:             json.RawMessage(`{"type":"string","enum":["block","classic"]}`),
			Default:            "block",
			Autoload:           true,
			Group:              GroupWriting,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.writing.post_by_email_enabled",
			Description:        "When true, messages sent to the inbound address are converted to draft posts.",
			Type:               SettingTypeBool,
			Schema:             json.RawMessage(`{"type":"boolean"}`),
			Default:            false,
			Autoload:           true,
			Group:              GroupWriting,
			RequiresCapability: policy.CapManageOptions,
		},
		{
			Key:                "core.writing.post_by_email_address",
			Description:        "Inbound mailbox that receives incoming post-by-email messages. Only used when post_by_email_enabled is true.",
			Type:               SettingTypeString,
			Schema:             json.RawMessage(`{"type":"string","maxLength":254}`),
			Default:            "",
			Autoload:           true,
			Group:              GroupWriting,
			RequiresCapability: policy.CapManageOptions,
		},
	}
}

// RegisterCore registers every Setting returned by CoreSettings into
// reg. Returns the first error encountered (registration is atomic
// per-setting, so a partial seed is possible on error — callers should
// treat any error as fatal at boot).
//
// Call this once per *Registry, typically at process startup before
// any plugins are activated.
func RegisterCore(reg *Registry) error {
	for _, s := range CoreSettings() {
		if err := reg.Register(s); err != nil {
			return err
		}
	}
	return nil
}
