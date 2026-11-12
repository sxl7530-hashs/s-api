package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/pkg/bedrockmodel"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ResolveBedrockModelLimits produces a request-local copy. Legacy friendly-name
// entries and explicit AWS IDs resolve to the same bucket without changing the
// saved channel settings. Save-time validation rejects conflicting aliases.
func ResolveBedrockModelLimits(settings *dto.BedrockProtectionSettings, mapping map[string]string, region string) (*dto.BedrockProtectionSettings, error) {
	if settings == nil || !settings.ModelRateLimitEnabled {
		return settings, nil
	}
	copy := *settings
	copy.ModelLimits = make(map[string]dto.BedrockModelLimit, len(settings.ModelLimits))
	for name, limit := range settings.ModelLimits {
		mapped, err := bedrockmodel.ResolveMapping(name, mapping)
		if err != nil {
			return nil, err
		}
		target := bedrockmodel.FinalID(mapped, region)
		if limit.ResolvedModel != "" && limit.ResolvedModel != target {
			return nil, fmt.Errorf("Bedrock model target changed for %q; refresh its quota or reselect the model", name)
		}
		if limit.QuotaRegion != "" && limit.QuotaRegion != region {
			return nil, fmt.Errorf("Bedrock quota region changed for %q; refresh its quota", name)
		}
		if old, exists := copy.ModelLimits[target]; exists && (old.RPM != limit.RPM || old.TPM != limit.TPM) {
			return nil, fmt.Errorf("conflicting Bedrock limits for aliases of %q", target)
		}
		copy.ModelLimits[target] = limit
	}
	return &copy, nil
}
