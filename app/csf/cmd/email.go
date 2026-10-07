package main

import (
	"github.com/candacelabs/csf/services/email"
	"github.com/prometheus/client_golang/prometheus"
)

const operatorEmailConfigFlag = "operator-email-config"

func configuredOperatorEmail(path string, registry *prometheus.Registry) (*email.Mailer, error) {
	return email.NewOperatorMailer(path, registry)
}
