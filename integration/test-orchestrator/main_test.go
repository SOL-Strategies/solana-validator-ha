package main

import "testing"

func TestValidatorRoleFromMetricsUsesGlobalMetadataOnly(t *testing.T) {
	metrics := `# HELP solana_validator_ha_profile_metadata Profile metadata
solana_validator_ha_profile_metadata{profile="main",public_ip="10.0.0.101",validator_name="validator-2",validator_role="active"} 1
# HELP solana_validator_ha_metadata Metadata about the validator HA manager
solana_validator_ha_metadata{public_ip="10.0.0.101",validator_name="validator-2",validator_role="passive",validator_status="healthy"} 1
`

	if got := validatorRoleFromMetrics(metrics); got != "passive" {
		t.Fatalf("validatorRoleFromMetrics() = %q, want passive", got)
	}
}

func TestValidatorRoleFromMetricsUnknownWithoutGlobalMetadata(t *testing.T) {
	metrics := `solana_validator_ha_profile_metadata{profile="main",validator_role="active"} 1`

	if got := validatorRoleFromMetrics(metrics); got != "unknown" {
		t.Fatalf("validatorRoleFromMetrics() = %q, want unknown", got)
	}
}
