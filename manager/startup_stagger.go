package manager

import (
	"sort"
	"time"
)

type startupStaggerInput struct {
	ID              string
	Name            string
	ModelKey        string
	Exchange        string
	ScanInterval    time.Duration
	ConfiguredDelay time.Duration
}

func planStartupStagger(inputs []startupStaggerInput) map[string]time.Duration {
	delays := make(map[string]time.Duration, len(inputs))
	byInterval := make(map[time.Duration][]startupStaggerInput)

	for _, input := range inputs {
		if input.ConfiguredDelay > 0 {
			delays[input.ID] = input.ConfiguredDelay
			continue
		}
		byInterval[input.ScanInterval] = append(byInterval[input.ScanInterval], input)
	}

	for scanInterval, list := range byInterval {
		// Group by ModelKey
		modelGroups := make(map[string][]startupStaggerInput)
		for _, item := range list {
			modelGroups[item.ModelKey] = append(modelGroups[item.ModelKey], item)
		}

		// Sort model keys stably
		modelKeys := make([]string, 0, len(modelGroups))
		for k := range modelGroups {
			modelKeys = append(modelKeys, k)
		}
		sort.Strings(modelKeys)

		// For each model group, sort members stably by Name, then ID
		for _, k := range modelKeys {
			group := modelGroups[k]
			sort.Slice(group, func(i, j int) bool {
				if group[i].Name == group[j].Name {
					return group[i].ID < group[j].ID
				}
				return group[i].Name < group[j].Name
			})
			modelGroups[k] = group
		}

		// Compute delays with inter-model stagger to prevent simultaneous market requests
		for groupIndex, modelKey := range modelKeys {
			group := modelGroups[modelKey]
			for memberIndex, input := range group {
				delays[input.ID] = computeStaggerDelay(scanInterval, groupIndex, memberIndex)
			}
		}
	}

	return delays
}

func computeStaggerDelay(scanInterval time.Duration, groupIndex, memberIndex int) time.Duration {
	if scanInterval <= 1*time.Minute {
		return 0
	}

	switch scanInterval {
	case 15 * time.Minute:
		// 3 major phases (0m, 5m, 10m) separated by 5 minutes to space same-model traders.
		// Inter-model offset (groupIndex) and intra-phase offset (memberIndex / 3) distribute
		// different models and excess traders across distinct 1-minute buckets (0m..14m).
		phase := time.Duration(memberIndex%3) * 5 * time.Minute
		subOffset := time.Duration(groupIndex+memberIndex/3) * time.Minute
		return (phase + subOffset) % scanInterval

	case 60 * time.Minute:
		// Base phases (20m, 40m) separated by 20 minutes for hourly traders.
		phase := time.Duration(memberIndex%2+1) * 20 * time.Minute
		subOffset := time.Duration(groupIndex+memberIndex/2) * time.Minute
		return (phase + subOffset) % scanInterval

	default:
		if scanInterval >= 15*time.Minute {
			step := 5 * time.Minute
			numPhases := int(scanInterval / step)
			if numPhases <= 0 {
				numPhases = 1
			}
			phase := time.Duration(memberIndex%numPhases) * step
			subOffset := time.Duration(groupIndex+memberIndex/numPhases) * time.Minute
			return (phase + subOffset) % scanInterval
		}
		return time.Duration(groupIndex*2+memberIndex) * time.Minute % scanInterval
	}
}
