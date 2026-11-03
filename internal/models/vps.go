package models

type VPSStatus string

const (
	VPSStatusActive   VPSStatus = "Active"
	VPSStatusDraining VPSStatus = "Draining"
	VPSStatusDisabled VPSStatus = "Disabled"
)

type VPS struct {
	IP     string
	User   string
	Status VPSStatus

	// Capacity
	MaxTunnels      int // Admin configurable field.
	AssignedTunnels int
	ActiveTunnels   int

	// Port allocation
	AllocatedTCPPorts int
	AllocatedUDPPorts int

	// Network
	RxMbps           float64
	TxMbps           float64
	PacketsPerSecond uint64

	// System
	CPUUsage    float64
	MemoryUsage float64
}

func CreateVPS(ip, user string, maxTunnels int) *VPS {
	return &VPS{
		IP:         ip,
		User:       user,
		Status:     VPSStatusActive,
		MaxTunnels: maxTunnels,
	}
}
