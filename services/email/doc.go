// Package email provides a composable, host-configured email capability.
//
// The host owns transport credentials, sender and recipients, provenance, and
// durable receipt retention. Callers provide only bounded message content.
// Mailer starts no goroutines and opens no network connection until Send.
package email
