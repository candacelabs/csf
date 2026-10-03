// Package deploy contains the small, durable domain model shared by
// Deploy controllers and user-facing applications.
//
// The package deliberately contains no scheduler, persistence, or transport
// concerns. Placement is a pure decision over an authoritative cluster
// snapshot, and receipts can only be appended to a ReceiptLog.
package deploy
