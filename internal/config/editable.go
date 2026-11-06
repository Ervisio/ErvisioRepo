package config

import "strings"

// Editable lists the settings an administrator may change at run time, from
// the web admin or the Ervisio plugin, as "section.key". None holds a secret.
var Editable = []string{
	"server.public_url", "server.ssh_listen", "server.trusted_proxies",
	"log.level", "log.format",
	"security.registration", "security.user_directory",
	"auth.password_min_length", "auth.session_idle_timeout", "auth.session_max_age",
	"auth.require_verified_email", "auth.login_free_failures_account", "auth.login_free_failures_ip",
	"auth.login_backoff_base", "auth.login_backoff_max", "auth.actions_per_ip_per_hour", "auth.require_2fa",
	"auth.token_max_ttl", "auth.invite_ttl", "auth.verify_token_ttl", "auth.reset_token_ttl",
	"mail.enabled", "mail.host", "mail.port", "mail.tls", "mail.username", "mail.password_file",
	"mail.from", "mail.helo_name", "mail.timeout",
	"git.max_push_size", "git.idle_timeout", "git.max_duration", "git.requests_per_hour",
	"ssh.enabled", "ssh.max_connections", "ssh.login_grace_time",
	"actions.enabled", "actions.default_actions_url", "actions.max_job_timeout",
	"actions.runner_offline_after", "actions.registration_token_ttl", "actions.max_jobs_per_run",
	"actions.cache_max_repo_size", "actions.cache_ttl", "actions.artifact_retention",
	"webhooks.allow_private", "webhooks.allowed_cidrs", "webhooks.timeout", "webhooks.max_attempts",
	"ui.default_layout",
}

// EnvName returns the environment variable that overrides the "section.key"
// setting, e.g. EREPO_AUTH_REQUIRE_2FA.
func EnvName(path string) string {
	return envPrefix + "_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}
