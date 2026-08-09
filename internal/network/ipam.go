package network

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type ipamAllocation struct {
	NetworkName string         `json:"network_name"`
	Segments    map[string]int `json:"segments"`
	NextIndex   int            `json:"last_index"`
}

func (a *ipamAllocation) allocateForCluster(clusterName string, maxSegments int) (int, error) {
	if a.NextIndex >= maxSegments {
		return 0, fmt.Errorf("no more available segments (max %d)", maxSegments)
	}
	index := a.NextIndex
	a.Segments[clusterName] = index
	a.NextIndex++
	return index, nil
}

func (a *ipamAllocation) deallocateCluster(clusterName string) {
	delete(a.Segments, clusterName)
}

func loadIPAMAllocation(networkName string) (*ipamAllocation, error) {
	stateDir := bridgeStateDir()
	path := filepath.Join(stateDir, fmt.Sprintf("ipam-%s.json", networkName))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &ipamAllocation{
			NetworkName: networkName,
			Segments:    make(map[string]int),
			NextIndex:   0,
		}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var alloc ipamAllocation
	if err := json.Unmarshal(data, &alloc); err != nil {
		return nil, err
	}
	return &alloc, nil
}

func (a *ipamAllocation) save() error {
	stateDir := bridgeStateDir()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(stateDir, fmt.Sprintf("ipam-%s.json", a.NetworkName))
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func computeIPAMRange(subnet string, index int, segmentSize int, segmentCount int) (string, string, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", "", fmt.Errorf("invalid CIDR %s: %w", subnet, err)
	}

	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	if hostBits < 2 {
		return "", "", fmt.Errorf("subnet too small for segmentation")
	}

	baseIP := ipnet.IP.To4()
	if baseIP == nil {
		return "", "", fmt.Errorf("only IPv4 supported")
	}

	reservedCount := segmentCount * 6
	maxUsableIP := 256 - reservedCount - 1

	offset := index*segmentSize + 2
	startIP := addToIP(baseIP, offset)
	endOffset := min(offset+segmentSize-3, maxUsableIP)
	endIP := addToIP(baseIP, endOffset)

	return startIP.String(), endIP.String(), nil
}

func addToIP(ip net.IP, offset int) net.IP {
	result := make(net.IP, len(ip))
	copy(result, ip)
	carry := offset
	for i := len(result) - 1; i >= 0 && carry > 0; i-- {
		sum := int(result[i]) + carry
		result[i] = byte(sum % 256)
		carry = sum / 256
	}
	return result
}

func computeReservedIPRange(subnet string, segmentCount int) (string, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR %s: %w", subnet, err)
	}

	baseIP := ipnet.IP.To4()
	if baseIP == nil {
		return "", fmt.Errorf("only IPv4 supported")
	}

	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	if hostBits < 2 {
		return "", fmt.Errorf("subnet too small")
	}

	reservedCount := segmentCount * 6

	reservedStart := addToIP(baseIP, 256-reservedCount-1)
	reservedEnd := addToIP(baseIP, 254)

	return fmt.Sprintf("%s-%s", reservedStart.String(), reservedEnd.String()), nil
}
