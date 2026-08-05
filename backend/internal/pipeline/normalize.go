package pipeline

import (
	"context"
	"time"
)

type NormalizeStage struct{}

func (NormalizeStage) StageID() string { return "normalize" }
func (NormalizeStage) Name() string    { return "接收规范化" }

func (NormalizeStage) Run(_ context.Context, st *State) error {
	if st.Alert.CreatedAt.IsZero() {
		st.Alert.CreatedAt = time.Now()
	}
	if st.Alert.FiredAt.IsZero() {
		st.Alert.FiredAt = time.Now()
	}
	if st.Alert.Status == "" {
		st.Alert.Status = "active"
	}
	return nil
}
