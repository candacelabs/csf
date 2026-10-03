// Copyright 2026 Candace Labs

// Package jobsmock holds generated gomock doubles of every exported jobs
// interface, for specs that exercise the ledger through its public API.
package jobsmock

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../database.go -destination=database.gen.go -package=jobsmock
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../executor.go -destination=executor.gen.go -package=jobsmock
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../batch.go -destination=batch.gen.go -package=jobsmock
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=../docker.go -destination=docker.gen.go -package=jobsmock
