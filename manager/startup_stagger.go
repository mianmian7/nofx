package manager

import (
	"sort"
	"time"
)

type startupStaggerInput struct {
	ID              string
	Name            string
	ModelKey        string
	ScanInterval    time.Duration
	ConfiguredDelay time.Duration
}

func planStartupStagger(inputs []startupStaggerInput) map[string]time.Duration {
	delays := make(map[string]time.Duration, len(inputs))
	groups := make(map[string][]startupStaggerInput)
	for _, input := range inputs {
		if input.ConfiguredDelay > 0 {
			delays[input.ID] = input.ConfiguredDelay
			continue
		}
		groupKey := input.ModelKey + "\x00" + input.ScanInterval.String()
		groups[groupKey] = append(groups[groupKey], input)
	}

	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			if group[i].Name == group[j].Name {
				return group[i].ID < group[j].ID
			}
			return group[i].Name < group[j].Name
		})
		for index, input := range group {
			delays[input.ID] = automaticStartupPhase(input.ScanInterval, index)
		}
	}
	return delays
}

func automaticStartupPhase(scanInterval time.Duration, index int) time.Duration {
	switch scanInterval {
	case 15 * time.Minute:
		return time.Duration(index%3) * 5 * time.Minute
	case 60 * time.Minute:
		return time.Duration(index%2+1) * 20 * time.Minute
	}
	if scanInterval <= 5*time.Minute {
		return 0
	}
	return time.Duration(index) * 5 * time.Minute % scanInterval
}
