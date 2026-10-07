package config

// NotifyConfig selects and configures the operator Notifier. SMTPPass is
// never read from YAML (tag "-"); it comes from the SMTP_PASS env var only,
// so the password never has to live in a config file on disk.
type NotifyConfig struct {
	Mode     string   `yaml:"mode"` // smtp|log|file
	File     string   `yaml:"file"` // path for mode=file
	SMTPHost string   `yaml:"smtp_host"`
	SMTPPort int      `yaml:"smtp_port"`
	SMTPUser string   `yaml:"smtp_user"`
	SMTPPass string   `yaml:"-"` // env SMTP_PASS only, never YAML
	SMTPFrom string   `yaml:"smtp_from"`
	SMTPTo   []string `yaml:"smtp_to"`
}
