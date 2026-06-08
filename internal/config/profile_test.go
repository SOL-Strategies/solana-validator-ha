package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	profileActivePubkeyA     = "11111111111111111111111111111111"
	profileActivePubkeyB     = "SysvarC1ock11111111111111111111111111111111"
	profileVotePubkeyA       = "Vote111111111111111111111111111111111111111"
	profileVotePubkeyB       = "Stake11111111111111111111111111111111111111"
	profileAuthorizedVoterA  = "SysvarRent111111111111111111111111111111111"
	profileAuthorizedVoterB  = "SysvarRecentB1ockHashes11111111111111111111"
	profileSelfValidatorName = "backup-1"
	profileSelfIP            = "10.0.0.99"
)

func boolPtr(v bool) *bool { return &v }

func validProfile(priority int, activePubkey, votePubkey, authorizedVoter string) Profile {
	return Profile{
		Priority: intPtr(priority),
		Identities: ValidatorIdentities{
			ActivePubkeyStr: activePubkey,
		},
		VotePubkeyStr:         votePubkey,
		AuthorizedVoterPubkey: authorizedVoter,
		Peers: Peers{
			profileSelfValidatorName: {IP: profileSelfIP, Name: profileSelfValidatorName},
			"primary":                {IP: "10.0.0.10", Name: "primary"},
		},
	}
}

func validProfiles() Profiles {
	return Profiles{
		"validator-a": validProfile(0, profileActivePubkeyA, profileVotePubkeyA, profileAuthorizedVoterA),
		"validator-b": validProfile(1, profileActivePubkeyB, profileVotePubkeyB, profileAuthorizedVoterB),
	}
}

func TestProfilesValidate(t *testing.T) {
	t.Run("valid profiles", func(t *testing.T) {
		assert.NoError(t, validProfiles().Validate())
	})

	t.Run("profiles required", func(t *testing.T) {
		err := Profiles{}.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "profiles must be defined")
	})

	t.Run("at least one enabled profile required", func(t *testing.T) {
		profile := validProfile(0, profileActivePubkeyA, profileVotePubkeyA, profileAuthorizedVoterA)
		profile.Enabled = boolPtr(false)
		err := Profiles{"disabled": profile}.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one enabled profile")
	})

	t.Run("duplicate active pubkey rejected", func(t *testing.T) {
		profiles := validProfiles()
		profile := profiles["validator-b"]
		profile.Identities.ActivePubkeyStr = profileActivePubkeyA
		profiles["validator-b"] = profile
		err := profiles.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "active identity")
	})

	t.Run("duplicate vote pubkey rejected", func(t *testing.T) {
		profiles := validProfiles()
		profile := profiles["validator-b"]
		profile.VotePubkeyStr = profileVotePubkeyA
		profiles["validator-b"] = profile
		err := profiles.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vote_pubkey")
	})

	t.Run("duplicate authorized voter rejected", func(t *testing.T) {
		profiles := validProfiles()
		profile := profiles["validator-b"]
		profile.AuthorizedVoterPubkey = profileAuthorizedVoterA
		profiles["validator-b"] = profile
		err := profiles.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authorized_voter")
	})

	t.Run("duplicate profile priority rejected", func(t *testing.T) {
		profiles := validProfiles()
		profile := profiles["validator-b"]
		profile.Priority = intPtr(0)
		profiles["validator-b"] = profile
		err := profiles.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "priority 0 is already used")
	})
}

func TestProfileValidatePeerPriorities(t *testing.T) {
	t.Run("partial peer priorities rejected", func(t *testing.T) {
		profile := validProfile(0, profileActivePubkeyA, profileVotePubkeyA, profileAuthorizedVoterA)
		profile.Name = "validator-a"
		profile.Peers[profileSelfValidatorName] = Peer{IP: profileSelfIP, Priority: intPtr(0)}
		err := profile.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "either all peers must declare a priority")
	})

	t.Run("duplicate peer priorities rejected", func(t *testing.T) {
		profile := validProfile(0, profileActivePubkeyA, profileVotePubkeyA, profileAuthorizedVoterA)
		profile.Name = "validator-a"
		profile.Peers[profileSelfValidatorName] = Peer{IP: profileSelfIP, Priority: intPtr(0)}
		profile.Peers["primary"] = Peer{IP: "10.0.0.10", Priority: intPtr(0)}
		err := profile.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already used")
	})
}

func TestProfilesValidateSelfPeer(t *testing.T) {
	t.Run("self peer present", func(t *testing.T) {
		assert.NoError(t, validProfiles().ValidateSelfPeer(profileSelfValidatorName, profileSelfIP))
	})

	t.Run("self peer missing", func(t *testing.T) {
		profiles := validProfiles()
		profile := profiles["validator-a"]
		delete(profile.Peers, profileSelfValidatorName)
		profiles["validator-a"] = profile
		err := profiles.ValidateSelfPeer(profileSelfValidatorName, profileSelfIP)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must include this node")
	})

	t.Run("self peer IP mismatch", func(t *testing.T) {
		profiles := validProfiles()
		err := profiles.ValidateSelfPeer(profileSelfValidatorName, "10.0.0.100")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must match this node's discovered public IP")
	})

	t.Run("self IP duplicated by another peer", func(t *testing.T) {
		profiles := validProfiles()
		profile := profiles["validator-a"]
		profile.Peers["duplicate-self-ip"] = Peer{IP: profileSelfIP}
		profiles["validator-a"] = profile
		err := profiles.ValidateSelfPeer(profileSelfValidatorName, profileSelfIP)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than once")
	})
}

func TestProfilePeerIPPriorityRankMap(t *testing.T) {
	profile := validProfile(0, profileActivePubkeyA, profileVotePubkeyA, profileAuthorizedVoterA)
	assert.Nil(t, profile.PeerIPPriorityRankMap())

	profile.Peers[profileSelfValidatorName] = Peer{IP: profileSelfIP, Priority: intPtr(1)}
	profile.Peers["primary"] = Peer{IP: "10.0.0.10", Priority: intPtr(0)}
	rankMap := profile.PeerIPPriorityRankMap()
	require.NotNil(t, rankMap)
	assert.Equal(t, 0, rankMap["10.0.0.10"])
	assert.Equal(t, 1, rankMap[profileSelfIP])
}
