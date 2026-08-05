package pipeline

import (
	"context"

	"aiops/internal/mockdata"
	"aiops/internal/model"
)

type ContextStage struct{}

func (ContextStage) StageID() string { return "context" }
func (ContextStage) Name() string    { return "上下文收集" }

func (ContextStage) Run(_ context.Context, st *State) error {
	var dev model.DeviceInfo
	for _, d := range mockdata.Devices() {
		if d.Name == st.Alert.Device {
			dev = d
			break
		}
	}
	st.Context = &model.Context{
		DeviceInfo: dev,
		Neighbors:  mockdata.Neighbors(st.Alert.Device),
	}
	return nil
}
