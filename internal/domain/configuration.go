package domain

// Configuration is the portable, non-secret app state serialized into a .cnk backup.
// Browser profiles, cookies, reservations, and payment authentication data are
// deliberately outside this boundary.
type Configuration struct {
	Revision int64        `json:"-"`
	Presets  []Preset     `json:"presets"`
	Monitors []MonitorJob `json:"monitors"`
}
