package relay

import (
	"fmt"
	"testing"
	"time"
)

func TestClientPruningRemovesMatchingBlockedDeviceState(t *testing.T) {
	control := &controlPlane{
		active: make(map[string]map[uint64]*trackedSession),
		state: controlState{
			Version:        controlStateVersion,
			Clients:        make(map[string]*trackedClient),
			BlockedDevices: make(map[string]string),
		},
	}
	for index := 0; index < maxTrackedClients; index++ {
		deviceID := fmt.Sprintf("device_%04d", index)
		key := "primary:" + deviceID
		control.state.Clients[key] = &trackedClient{Key: key, TokenID: "primary",
			DeviceID: deviceID, LastSeen: fmt.Sprintf("%04d", index)}
	}
	oldest := "primary:device_0000"
	control.state.BlockedDevices[oldest] = time.Now().UTC().Format(time.RFC3339)

	control.pruneClientsLocked()

	if control.state.Clients[oldest] != nil {
		t.Fatal("oldest client was not pruned")
	}
	if _, exists := control.state.BlockedDevices[oldest]; exists {
		t.Fatal("pruned client left an invalid blocked-device record")
	}
}
