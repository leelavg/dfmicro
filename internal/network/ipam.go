package network

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
)

type clusterRange struct {
	Name       string `json:"name"`
	Index      int    `json:"index"`
	RangeStart string `json:"rangeStart"`
	RangeEnd   string `json:"rangeEnd"`
}

type groupAlloc struct {
	Name       string         `json:"name"`
	Clusters   []clusterRange `json:"clusters"`
	Index      int            `json:"index"`
	VlanID     int            `json:"vlanId"`
	RangeStart string         `json:"rangeStart"`
	RangeEnd   string         `json:"rangeEnd"`
}

type ipamManager struct {
	NetworkName string       `json:"networkName"`
	Subnet      string       `json:"subnet"`
	Groups      []groupAlloc `json:"groups"`
}

func freeSlot[T any](items []T, index func(T) int) int {
	for i, it := range items {
		if index(it) != i {
			return i
		}
	}
	return len(items)
}

func newIPAMManager(stateDir, networkName string) (*ipamManager, error) {
	path := filepath.Join(stateDir, fmt.Sprintf("ipam-%s.json", networkName))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ipamManager{
			NetworkName: networkName,
			Groups:      []groupAlloc{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	var alloc ipamManager
	if err := json.Unmarshal(data, &alloc); err != nil {
		return nil, err
	}
	return &alloc, nil
}

func (a *ipamManager) save(stateDir string) error {
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

func (a *ipamManager) getGroup(name string) (int, *groupAlloc, error) {
	for i := range a.Groups {
		if a.Groups[i].Name == name {
			return i, &a.Groups[i], nil
		}
	}
	return -1, nil, fmt.Errorf("group not found: %s", name)
}

func (a *ipamManager) addGroup(name string, subnet string, groupCount, reservePerGroup int) (*groupAlloc, error) {
	for i := range a.Groups {
		if a.Groups[i].Name == name {
			return &a.Groups[i], nil
		}
	}
	index := freeSlot(a.Groups, func(g groupAlloc) int { return g.Index })
	if index >= groupCount {
		return nil, fmt.Errorf("no more available groups (max %d)", groupCount)
	}

	a.Subnet = subnet
	rangeStart, rangeEnd, err := computeIPAMRange(subnet, index, groupCount, reservePerGroup)
	if err != nil {
		return nil, fmt.Errorf("failed to compute IPAM range for group %s: %w", name, err)
	}

	g := groupAlloc{
		Name:       name,
		Clusters:   []clusterRange{},
		Index:      index,
		VlanID:     10 + index,
		RangeStart: rangeStart,
		RangeEnd:   rangeEnd,
	}
	a.Groups = slices.Insert(a.Groups, index, g)
	return &a.Groups[index], nil
}

func (a *ipamManager) removeGroup(idx int) error {
	if idx < 0 || idx >= len(a.Groups) {
		return fmt.Errorf("invalid group index: %d", idx)
	}

	if len(a.Groups[idx].Clusters) == 0 {
		a.Groups = slices.Delete(a.Groups, idx, idx+1)
	}

	return nil
}

func (g *groupAlloc) addCluster(clusterName string, clustersPerGroup int) (*clusterRange, error) {
	for i := range g.Clusters {
		if g.Clusters[i].Name == clusterName {
			return &g.Clusters[i], nil
		}
	}

	index := freeSlot(g.Clusters, func(c clusterRange) int { return c.Index })
	if index >= clustersPerGroup {
		return nil, fmt.Errorf("group %s has reached max clusters (%d)", g.Name, clustersPerGroup)
	}

	clusterStart, clusterEnd, err := computeClusterSubrange(g.RangeStart, g.RangeEnd, index, clustersPerGroup)
	if err != nil {
		return nil, err
	}
	cr := clusterRange{
		Name:       clusterName,
		Index:      index,
		RangeStart: clusterStart,
		RangeEnd:   clusterEnd,
	}
	g.Clusters = slices.Insert(g.Clusters, index, cr)
	return &g.Clusters[index], nil
}

func (g *groupAlloc) removeCluster(clusterName string) error {
	for i := range g.Clusters {
		if g.Clusters[i].Name == clusterName {
			g.Clusters = slices.Delete(g.Clusters, i, i+1)
			return nil
		}
	}
	return fmt.Errorf("cluster %q not found in group %q", clusterName, g.Name)
}

func addToIP(ip net.IP, offset int) net.IP {
	result := make(net.IP, 4)
	binary.BigEndian.PutUint32(result, binary.BigEndian.Uint32(ip)+uint32(offset))
	return result
}

func parseSubnetBounds(subnet string, groupCount, reserverPerGroup int) (baseIP net.IP, lastUsable int, reservedCount int, err error) {
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
	reservedCount = groupCount * reserverPerGroup
	return baseIP, lastUsable, reservedCount, nil
}

func computeIPAMRange(subnet string, index, groupCount, reservePerGroup int) (string, string, error) {
	baseIP, lastUsable, reservedCount, err := parseSubnetBounds(subnet, groupCount, reservePerGroup)
	if err != nil {
		return "", "", err
	}
	segmentSize := (lastUsable + 2) / groupCount
	maxUsableIP := lastUsable - reservedCount
	offset := index*segmentSize + 2
	startIP := addToIP(baseIP, offset)
	endIP := addToIP(baseIP, min(offset+segmentSize-3, maxUsableIP))
	return startIP.String(), endIP.String(), nil
}

func computeReservedIPRange(subnet string, groupCount, reservePerGroup int) (string, error) {
	baseIP, lastUsable, reservedCount, err := parseSubnetBounds(subnet, groupCount, reservePerGroup)
	if err != nil {
		return "", err
	}
	if reservedCount >= lastUsable {
		return "", fmt.Errorf("subnet too small for %d segments", groupCount)
	}
	reservedStart := addToIP(baseIP, lastUsable-reservedCount+1)
	reservedEnd := addToIP(baseIP, lastUsable)
	return fmt.Sprintf("%s-%s", reservedStart.String(), reservedEnd.String()), nil
}

func computeClusterSubrange(groupStart, groupEnd string, clusterIdx, clustersPerGroup int) (string, string, error) {
	startIP := net.ParseIP(groupStart)
	if startIP == nil {
		return "", "", fmt.Errorf("invalid IP %s", groupStart)
	}
	endIP := net.ParseIP(groupEnd)
	if endIP == nil {
		return "", "", fmt.Errorf("invalid IP %s", groupEnd)
	}

	startNum := binary.BigEndian.Uint32(startIP.To4())
	endNum := binary.BigEndian.Uint32(endIP.To4())
	totalIPs := endNum - startNum + 1
	clusterSize := totalIPs / uint32(clustersPerGroup)

	clusterStartNum := startNum + uint32(clusterIdx)*clusterSize
	clusterEndNum := clusterStartNum + clusterSize - 1

	clusterStartIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(clusterStartIP, clusterStartNum)
	clusterEndIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(clusterEndIP, clusterEndNum)

	return clusterStartIP.String(), clusterEndIP.String(), nil
}
