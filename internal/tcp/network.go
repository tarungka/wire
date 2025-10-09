package tcp

import (
	// "net"
)

// NetworkReporter is a reporter for network information
type NetworkReporter struct{}

// Address is a struct for holding network addresses
type Address struct {
	Addr string `json:"address"`
}

// InterfaceDetail is a struct for holding network interface details
type InterfaceDetail struct {
	Flags           string    `json:"flags"`
	HardwareAddress string    `json:"hardware_address"`
	Addresses       []Address `json:"addresses"`
}

// InterfaceStats is a map of network interface names to InterfaceDetail
type InterfaceStats map[string]InterfaceDetail

// Stats returns network interface details
func (n NetworkReporter) Stats() (map[string]interface{}, error) {
	// TODO: Implementation truncated
	return nil, nil
}
