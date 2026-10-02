package config

import "github.com/anydoor7/tslink/internal/duration"

// DurationPolicyConfig bounds guest and public lifetimes. Its presence requires
// a positive relative public_max >= 1h; never and absolute values are invalid.
type DurationPolicyConfig struct {
	PublicMax string `json:"public_max"`
}

func (c GlobalConfig) LifetimePolicy() (duration.Policy, error) {
	p := duration.Policy{}
	if c.Durations != nil {
		d, err := duration.ParseRelative(c.Durations.PublicMax)
		if err != nil {
			return p, err
		}
		p.PublicMax = d
	}
	return p, p.Validate()
}

func LoadLifetimePolicy() (duration.Policy, error) {
	c, err := LoadGlobalConfig()
	if err != nil {
		return duration.Policy{}, err
	}
	return c.LifetimePolicy()
}
