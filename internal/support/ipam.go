package support

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type IPAMManager struct {
	NetworkName string         `json:"network_name"`
	Segments    map[string]int `json:"segments"`
	NextIndex   int            `json:"last_index"`
}

func NewIPAMManager(stateDir, networkName string) (*IPAMManager, error) {
	path := filepath.Join(stateDir, fmt.Sprintf("ipam-%s.json", networkName))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &IPAMManager{
			NetworkName: networkName,
			Segments:    make(map[string]int),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var alloc IPAMManager
	if err := json.Unmarshal(data, &alloc); err != nil {
		return nil, err
	}
	return &alloc, nil
}

func (a *IPAMManager) Save(stateDir string) error {
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

func (a *IPAMManager) AllocateForCluster(name string, maxSegments int) (int, error) {
	if idx, ok := a.Segments[name]; ok {
		return idx, nil
	}
	if a.NextIndex >= maxSegments {
		return 0, fmt.Errorf("no more available segments (max %d)", maxSegments)
	}
	index := a.NextIndex
	a.Segments[name] = index
	a.NextIndex++
	return index, nil
}

func (a *IPAMManager) DeallocateCluster(name string) {
	delete(a.Segments, name)
	a.NextIndex = len(a.Segments)
}

func addToIP(ip net.IP, offset int) net.IP {
	result := make(net.IP, 4)
	binary.BigEndian.PutUint32(result, binary.BigEndian.Uint32(ip)+uint32(offset))
	return result
}

func parseSubnetBounds(subnet string, segmentCount, reserverPerSegment int) (baseIP net.IP, lastUsable int, reservedCount int, err error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("invalid CIDR %s: %w", subnet, err)
	}
	baseIP = ipnet.IP.To4()
	if baseIP == nil {
		return nil, 0, 0, fmt.Errorf("only IPv4 supported")
	}
	ones, bits := ipnet.Mask.Size()
	hostBits := bits - ones
	if hostBits < 2 {
		return nil, 0, 0, fmt.Errorf("subnet too small")
	}
	hostCount := 1 << hostBits
	lastUsable = hostCount - 2
	reservedCount = segmentCount * reserverPerSegment
	return baseIP, lastUsable, reservedCount, nil
}

func ComputeIPAMRange(subnet string, index, segmentCount, reservePerSegment int) (string, string, error) {
	baseIP, lastUsable, reservedCount, err := parseSubnetBounds(subnet, segmentCount, reservePerSegment)
	if err != nil {
		return "", "", err
	}
	segmentSize := (lastUsable + 2) / segmentCount
	maxUsableIP := lastUsable - reservedCount
	offset := index*segmentSize + 2
	startIP := addToIP(baseIP, offset)
	endIP := addToIP(baseIP, min(offset+segmentSize-3, maxUsableIP))
	return startIP.String(), endIP.String(), nil
}

func ComputeReservedIPRange(subnet string, segmentCount, reservePerSegment int) (string, error) {
	baseIP, lastUsable, reservedCount, err := parseSubnetBounds(subnet, segmentCount, reservePerSegment)
	if err != nil {
		return "", err
	}
	if reservedCount >= lastUsable {
		return "", fmt.Errorf("subnet too small for %d segments", segmentCount)
	}
	reservedStart := addToIP(baseIP, lastUsable-reservedCount+1)
	reservedEnd := addToIP(baseIP, lastUsable)
	return fmt.Sprintf("%s-%s", reservedStart.String(), reservedEnd.String()), nil
}
