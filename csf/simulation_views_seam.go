//go:build !goverter

package csf

func init() { simulationViews = &iSimulationViewsImpl{} }
