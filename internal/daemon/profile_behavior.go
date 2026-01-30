package daemon

import (
	"strings"

	"github.com/agentlab/agentlab/internal/models"
	"gopkg.in/yaml.v3"
)

type profileBehaviorSpec struct {
	Behavior profileBehaviorDefaults `yaml:"behavior"`
}

type profileBehaviorDefaults struct {
	KeepaliveDefault  *bool `yaml:"keepalive_default"`
	TTLMinutesDefault *int  `yaml:"ttl_minutes_default"`
}

type behaviorDefaults struct {
	Keepalive  *bool
	TTLMinutes *int
}

func parseProfileBehaviorDefaults(raw string) (behaviorDefaults, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return behaviorDefaults{}, nil
	}
	var spec profileBehaviorSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return behaviorDefaults{}, err
	}
	defaults := behaviorDefaults{
		Keepalive:  spec.Behavior.KeepaliveDefault,
		TTLMinutes: spec.Behavior.TTLMinutesDefault,
	}
	if defaults.TTLMinutes != nil && *defaults.TTLMinutes <= 0 {
		defaults.TTLMinutes = nil
	}
	return defaults, nil
}

func applyProfileBehaviorDefaults(profile models.Profile, ttlMinutes *int, keepalive *bool) (int, bool, error) {
	defaults, err := parseProfileBehaviorDefaults(profile.RawYAML)
	if err != nil {
		return 0, false, err
	}
	effectiveTTL := 0
	if ttlMinutes != nil {
		effectiveTTL = *ttlMinutes
	} else if defaults.TTLMinutes != nil {
		effectiveTTL = *defaults.TTLMinutes
	}
	effectiveKeepalive := false
	if keepalive != nil {
		effectiveKeepalive = *keepalive
	} else if defaults.Keepalive != nil {
		effectiveKeepalive = *defaults.Keepalive
	}
	return effectiveTTL, effectiveKeepalive, nil
}
