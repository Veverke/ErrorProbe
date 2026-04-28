package stack

// Container names managed by errorprobe.
const (
	ContainerLoki    = "errorprobe-loki"
	ContainerGrafana = "errorprobe-grafana"
	ContainerVector  = "errorprobe-vector"

	NetworkName       = "errorprobe-net"
	VolumeLokiData    = "errorprobe-loki-data"
	VolumeGrafanaData = "errorprobe-grafana-data"
)
