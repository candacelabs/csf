package warden

// IIncidentLog exposes the incident history for the dashboard. Implemented
// by the watchdog. Returned slices are copies, most recent first.
type IIncidentLog interface {
	Incidents() []Incident
}
