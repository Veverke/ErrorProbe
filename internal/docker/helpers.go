package docker

import (
	"github.com/docker/docker/api/types/image"
	dockernat "github.com/docker/go-connections/nat"
)

func pullOptions() image.PullOptions {
	return image.PullOptions{}
}

// buildPortBindings converts []PortBinding to the Docker SDK nat.PortMap.
func buildPortBindings(ports []PortBinding) dockernat.PortMap {
	pm := dockernat.PortMap{}
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		containerPort := dockernat.Port(p.ContainerPort + "/" + proto)
		pm[containerPort] = []dockernat.PortBinding{
			{HostIP: "0.0.0.0", HostPort: p.HostPort},
		}
	}
	return pm
}

// buildExposedPorts converts []PortBinding to the Docker SDK nat.PortSet.
func buildExposedPorts(ports []PortBinding) dockernat.PortSet {
	ps := dockernat.PortSet{}
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		ps[dockernat.Port(p.ContainerPort+"/"+proto)] = struct{}{}
	}
	return ps
}
