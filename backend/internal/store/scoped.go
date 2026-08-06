package store

import (
	"aiops/internal/model"
	"aiops/internal/scope"
)

type ScopedStore struct {
	st *Store
	sc scope.Scope
}

func NewScoped(st *Store, sc scope.Scope) *ScopedStore {
	return &ScopedStore{st: st, sc: sc}
}

func (ss *ScopedStore) SaveStage(s model.StageResult) error {
	if ss.sc.Level != scope.LevelTask || s.RunID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveStage(s)
}

func (ss *ScopedStore) SaveRun(r model.PipelineRun) error {
	if ss.sc.Level != scope.LevelTask || r.ID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveRun(r)
}

func (ss *ScopedStore) UpdateRunStatus(runID, status string) error {
	if ss.sc.Level != scope.LevelTask || runID != ss.sc.TaskID {
		return scope.ErrScopeDenied
	}
	return ss.st.UpdateRunStatus(runID, status)
}

func (ss *ScopedStore) ListStages(runID string) ([]model.StageResult, error) {
	if ss.sc.Level == scope.LevelTask && runID != ss.sc.TaskID {
		return nil, scope.ErrScopeDenied
	}
	return ss.st.ListStages(runID)
}

func (ss *ScopedStore) GetLatestStage(runID, stageID string) (model.StageResult, bool, error) {
	if ss.sc.Level == scope.LevelTask && runID != ss.sc.TaskID {
		return model.StageResult{}, false, scope.ErrScopeDenied
	}
	return ss.st.GetLatestStage(runID, stageID)
}

func (ss *ScopedStore) ListAlerts() ([]model.Alert, error) {
	if ss.sc.Level == scope.LevelGlobal {
		return ss.st.ListAlerts()
	}
	return ss.st.ListAlertsByDevice(ss.sc.DeviceID)
}

func (ss *ScopedStore) GetAlert(id string) (model.Alert, error) {
	a, err := ss.st.GetAlert(id)
	if err != nil {
		return a, err
	}
	if ss.sc.Level != scope.LevelGlobal && a.Device != ss.sc.DeviceID {
		return a, scope.ErrScopeDenied
	}
	return a, nil
}

func (ss *ScopedStore) GetRun(runID string) (model.PipelineRun, error) {
	if ss.sc.Level == scope.LevelGlobal {
		return ss.st.GetRun(runID)
	}
	if ss.sc.Level == scope.LevelTask && runID == ss.sc.TaskID {
		return ss.st.GetRun(runID)
	}
	r, err := ss.st.GetRun(runID)
	if err != nil {
		return r, err
	}
	a, err := ss.st.GetAlert(r.AlertID)
	if err != nil {
		return r, err // run's alert missing -> not-found (sql.ErrNoRows), not a scope denial
	}
	if a.Device != ss.sc.DeviceID {
		return r, scope.ErrScopeDenied
	}
	return r, nil
}

func (ss *ScopedStore) SaveAlert(a model.Alert) error {
	if ss.sc.Level != scope.LevelGlobal && a.Device != ss.sc.DeviceID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveAlert(a)
}

func (ss *ScopedStore) GetDeviceContext(device string) (model.DeviceContext, error) {
	if ss.sc.Level != scope.LevelGlobal && device != ss.sc.DeviceID {
		return model.DeviceContext{}, scope.ErrScopeDenied
	}
	return ss.st.GetDeviceContext(device)
}

func (ss *ScopedStore) UpdateDeviceContext(dc model.DeviceContext) error {
	if ss.sc.Level != scope.LevelGlobal && dc.DeviceID != ss.sc.DeviceID {
		return scope.ErrScopeDenied
	}
	return ss.st.SaveDeviceContext(dc)
}
