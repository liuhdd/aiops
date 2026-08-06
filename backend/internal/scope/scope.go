package scope

import (
	"errors"
	"fmt"
	"strings"
)

var ErrScopeDenied = errors.New("scope denied")

type Level int

const (
	LevelGlobal Level = iota
	LevelDevice
	LevelTask
)

type Scope struct {
	Level    Level
	TaskID   string
	DeviceID string
}

func Global() Scope { return Scope{Level: LevelGlobal} }

func Device(name string) Scope { return Scope{Level: LevelDevice, DeviceID: name} }

func Task(runID, device string) Scope {
	return Scope{Level: LevelTask, TaskID: runID, DeviceID: device}
}

func (sc Scope) Encode() string {
	switch sc.Level {
	case LevelDevice:
		return "device:" + sc.DeviceID
	case LevelTask:
		return "task:" + sc.TaskID
	default:
		return "global"
	}
}

func Parse(s string) (Scope, error) {
	switch {
	case s == "" || s == "global":
		return Global(), nil
	case strings.HasPrefix(s, "device:"):
		return Device(strings.TrimPrefix(s, "device:")), nil
	case strings.HasPrefix(s, "task:"):
		return Task(strings.TrimPrefix(s, "task:"), ""), nil
	}
	return Scope{}, fmt.Errorf("unknown scope %q", s)
}
