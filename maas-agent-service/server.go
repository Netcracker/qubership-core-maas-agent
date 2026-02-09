package main

import (
	_ "github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/config"
	service "github.com/netcracker/qubership-core-maas-agent/maas-agent-service/v2/lib"

	// memlimit sets memory limit = 0.9 of cgroup memory limit
	_ "github.com/netcracker/qubership-core-lib-go/v3/memlimit"
)

func main() {
	service.RunServer()
}
