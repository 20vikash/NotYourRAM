package core

import (
	"errors"
	"math"
	"time"

	models "github.com/20vikash/NotYourRAM/internal/models"
)

type PlacementPolicy struct {
	MaxCPUUsage     float64
	MaxMemoryUsage  float64
	MaxNetworkUsage float64
	MaxPPSUsage     float64

	Weights PlacementWeights
}

type PlacementWeights struct {
	Network float64
	CPU     float64
	PPS     float64
	Tunnels float64
	Memory  float64
}

func (p PlacementPolicy) Validate() error {
	thresholds := []float64{
		p.MaxCPUUsage,
		p.MaxMemoryUsage,
		p.MaxNetworkUsage,
		p.MaxPPSUsage,
	}

	for _, threshold := range thresholds {
		if math.IsNaN(threshold) ||
			math.IsInf(threshold, 0) ||
			threshold <= 0 ||
			threshold > 100 {
			return errors.New("placement thresholds must be between 0 and 100")
		}
	}

	weights := []float64{
		p.Weights.Network,
		p.Weights.CPU,
		p.Weights.PPS,
		p.Weights.Tunnels,
		p.Weights.Memory,
	}

	total := 0.0

	for _, weight := range weights {
		if math.IsNaN(weight) ||
			math.IsInf(weight, 0) ||
			weight < 0 {
			return errors.New("placement weights must be finite and non-negative")
		}

		total += weight
	}

	if total <= 0 {
		return errors.New("at least one placement weight must be positive")
	}

	return nil
}

func SelectVPS(
	vpsList []*models.VPS,
	policy PlacementPolicy,
) (*models.VPS, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}

	var selected *models.VPS
	bestScore := math.Inf(1)

	totalWeight := policy.Weights.Network +
		policy.Weights.CPU +
		policy.Weights.PPS +
		policy.Weights.Tunnels +
		policy.Weights.Memory

	for _, vps := range vpsList {
		if vps == nil || vps.Status != models.VPSStatusActive {
			continue
		}

		// Validate configured capacities.
		if vps.MaxTunnels <= 0 ||
			vps.NetworkCapacityMbps <= 0 ||
			vps.SustainablePPS == 0 {
			continue
		}

		// Reject invalid metrics.
		if !validPercentage(vps.CPUUsage) ||
			!validPercentage(vps.MemoryUsage) ||
			!validNonNegative(vps.RxMbps) ||
			!validNonNegative(vps.TxMbps) ||
			vps.AssignedTunnels < 0 {
			continue
		}

		// Tunnel capacity.
		if vps.AssignedTunnels >= vps.MaxTunnels {
			continue
		}

		// System utilization.
		if vps.CPUUsage >= policy.MaxCPUUsage ||
			vps.MemoryUsage >= policy.MaxMemoryUsage {
			continue
		}

		// Network utilization.
		network := math.Max(
			vps.RxMbps,
			vps.TxMbps,
		) / vps.NetworkCapacityMbps

		// PPS utilization based on bootstrap benchmark.
		pps := float64(vps.PacketsPerSecond) /
			float64(vps.SustainablePPS)

		// Reject overloaded relays.
		if network >= policy.MaxNetworkUsage/100 ||
			pps >= policy.MaxPPSUsage/100 {
			continue
		}

		// Normalize remaining metrics.
		cpu := vps.CPUUsage / 100
		memory := vps.MemoryUsage / 100

		tunnels := float64(vps.AssignedTunnels) /
			float64(vps.MaxTunnels)

		// Weighted placement score.
		score := (policy.Weights.Network*network +
			policy.Weights.CPU*cpu +
			policy.Weights.PPS*pps +
			policy.Weights.Tunnels*tunnels +
			policy.Weights.Memory*memory) / totalWeight

		if score < bestScore {
			bestScore = score
			selected = vps
		}
	}

	if selected == nil {
		// TODO: Automatically provision another VPS using
		// the cloud provider SDK when capacity is exhausted.
		return nil, errors.New(
			"no VPS available for tunnel placement. Create another one",
		)
	}

	return selected, nil
}

func CreateTunnel(
	id string,
	accountID string,
	publicKey string,
	privateIP string,
	allowedIPs string,
	vpsList []*models.VPS,
	policy PlacementPolicy,
) (*models.Tunnel, error) {

	if id == "" ||
		accountID == "" ||
		publicKey == "" ||
		privateIP == "" ||
		allowedIPs == "" {
		return nil, errors.New("missing required tunnel fields")
	}

	vps, err := SelectVPS(vpsList, policy)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	return &models.Tunnel{
		ID:         id,
		AccountID:  accountID,
		VPS:        vps,
		PublicKey:  publicKey,
		PrivateIP:  privateIP,
		AllowedIPs: allowedIPs,
		Status:     models.TunnelStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func validPercentage(value float64) bool {
	return !math.IsNaN(value) &&
		!math.IsInf(value, 0) &&
		value >= 0 &&
		value <= 100
}

func validNonNegative(value float64) bool {
	return !math.IsNaN(value) &&
		!math.IsInf(value, 0) &&
		value >= 0
}
