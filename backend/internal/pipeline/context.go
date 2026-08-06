package pipeline

import (
	"context"
	"time"

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
	if st.Store != nil {
		_ = st.Store.UpdateDeviceContext(model.DeviceContext{
			DeviceID:  st.Alert.Device,
			StateJSON: model.MustJSON(dev),
			UpdatedBy: st.Run.ID,
			UpdatedAt: time.Now(),
		})
		if dc, err := st.Store.GetDeviceContext(st.Alert.Device); err == nil {
			st.DeviceContext = &dc
		}
	}
	return nil
}
