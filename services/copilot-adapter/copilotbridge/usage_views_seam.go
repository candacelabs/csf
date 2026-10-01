//go:build !goverter

package copilotbridge

func init() { usageViews = &iUsageConverterImpl{} }
