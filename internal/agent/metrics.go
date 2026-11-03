package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"golang.zx2c4.com/wireguard/wgctrl"
)

type Metrics struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage"`

	RxMbps float64 `json:"rx_mbps"`
	TxMbps float64 `json:"tx_mbps"`

	RxPPS            float64 `json:"rx_pps"`
	TxPPS            float64 `json:"tx_pps"`
	PacketsPerSecond float64 `json:"packets_per_second"`

	Tunnels map[string]TunnelMetrics `json:"tunnels"`

	CollectedAt time.Time `json:"collected_at"`
}

type TunnelMetrics struct {
	PublicKey string `json:"public_key"`

	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`

	RxBytesPerSecond float64 `json:"rx_bytes_per_second"`
	TxBytesPerSecond float64 `json:"tx_bytes_per_second"`
}

type networkSample struct {
	BytesRecv   uint64
	BytesSent   uint64
	PacketsRecv uint64
	PacketsSent uint64
}

type peerSample struct {
	RxBytes uint64
	TxBytes uint64
}

type Collector struct {
	Interface string
	WireGuard string
	Interval  time.Duration
}

func NewCollector(
	networkInterface string,
	wireguardInterface string,
) *Collector {
	return &Collector{
		Interface: networkInterface,
		WireGuard: wireguardInterface,
		Interval:  time.Second,
	}
}

func (c *Collector) Collect(ctx context.Context) (*Metrics, error) {
	if c.Interface == "" || c.WireGuard == "" {
		return nil, errors.New("network and WireGuard interfaces are required")
	}

	if c.Interval <= 0 {
		return nil, errors.New("collection interval must be positive")
	}

	// First sample.
	firstNetwork, err := c.readNetwork()
	if err != nil {
		return nil, err
	}

	firstPeers, err := c.readPeers()
	if err != nil {
		return nil, err
	}

	firstTime := time.Now()

	// Measure CPU and network activity over the same interval.
	cpuValues, err := cpu.PercentWithContext(ctx, c.Interval, false)
	if err != nil {
		return nil, fmt.Errorf("collect CPU usage: %w", err)
	}

	if len(cpuValues) == 0 {
		return nil, errors.New("no CPU metrics returned")
	}

	// Second sample.
	secondNetwork, err := c.readNetwork()
	if err != nil {
		return nil, err
	}

	secondPeers, err := c.readPeers()
	if err != nil {
		return nil, err
	}

	elapsed := time.Since(firstTime).Seconds()
	if elapsed <= 0 {
		return nil, errors.New("invalid collection interval")
	}

	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect memory usage: %w", err)
	}

	metrics := &Metrics{
		CPUUsage:    cpuValues[0],
		MemoryUsage: memory.UsedPercent,

		RxMbps: rate(secondNetwork.BytesRecv, firstNetwork.BytesRecv, elapsed) * 8 / 1_000_000,
		TxMbps: rate(secondNetwork.BytesSent, firstNetwork.BytesSent, elapsed) * 8 / 1_000_000,

		RxPPS: rate(secondNetwork.PacketsRecv, firstNetwork.PacketsRecv, elapsed),
		TxPPS: rate(secondNetwork.PacketsSent, firstNetwork.PacketsSent, elapsed),

		Tunnels: make(map[string]TunnelMetrics),

		CollectedAt: time.Now().UTC(),
	}

	metrics.PacketsPerSecond = metrics.RxPPS + metrics.TxPPS

	for publicKey, current := range secondPeers {
		previous, exists := firstPeers[publicKey]

		tunnel := TunnelMetrics{
			PublicKey: publicKey,
			RxBytes:   current.RxBytes,
			TxBytes:   current.TxBytes,
		}

		if exists {
			tunnel.RxBytesPerSecond = rate(
				current.RxBytes,
				previous.RxBytes,
				elapsed,
			)

			tunnel.TxBytesPerSecond = rate(
				current.TxBytes,
				previous.TxBytes,
				elapsed,
			)
		}

		metrics.Tunnels[publicKey] = tunnel
	}

	return metrics, nil
}

func (c *Collector) readNetwork() (*networkSample, error) {
	stats, err := net.IOCounters(true)
	if err != nil {
		return nil, fmt.Errorf("read network counters: %w", err)
	}

	for _, stat := range stats {
		if stat.Name == c.Interface {
			return &networkSample{
				BytesRecv:   stat.BytesRecv,
				BytesSent:   stat.BytesSent,
				PacketsRecv: stat.PacketsRecv,
				PacketsSent: stat.PacketsSent,
			}, nil
		}
	}

	return nil, fmt.Errorf("network interface %q not found", c.Interface)
}

func (c *Collector) readPeers() (map[string]peerSample, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("open WireGuard client: %w", err)
	}
	defer client.Close()

	device, err := client.Device(c.WireGuard)
	if err != nil {
		return nil, fmt.Errorf("read WireGuard device: %w", err)
	}

	peers := make(map[string]peerSample, len(device.Peers))

	for _, peer := range device.Peers {
		peers[peer.PublicKey.String()] = peerSample{
			RxBytes: uint64(peer.ReceiveBytes),
			TxBytes: uint64(peer.TransmitBytes),
		}
	}

	return peers, nil
}

// rate calculates the per-second increase of a cumulative counter.
// A reset or counter decrease returns zero.
func rate(current, previous uint64, seconds float64) float64 {
	if current < previous || seconds <= 0 {
		return 0
	}

	return float64(current-previous) / seconds
}
