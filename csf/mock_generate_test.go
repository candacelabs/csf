package csf_test

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=simulations.go -destination=internal/mocks/simulations.gen.go -package=csfmocks

//go:generate go run ../tools/generateopensearch
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=knowledge.go -destination=internal/mocks/knowledge.gen.go -package=csfmocks
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=service.go -destination=internal/mocks/http.gen.go -package=csfmocks

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=simulation_local.go -destination=internal/mocks/simulation_local.gen.go -package=csfmocks

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=simulation_traces.go -destination=internal/mocks/simulation_traces.gen.go -package=csfmocks
