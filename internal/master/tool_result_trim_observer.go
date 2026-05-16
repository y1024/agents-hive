package master

import (
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/compaction"
	"github.com/chef-guo/agents-hive/internal/tools"
)

type toolsReadTrackerTrimObserver struct{}

func (toolsReadTrackerTrimObserver) OnToolResultTrimmed(event compaction.ToolResultTrimEvent) {
	for _, path := range readTrackerPathsFromTrimEvent(event) {
		tools.RemoveReadTrackerPath(path)
	}
}

func readTrackerPathsFromTrimEvent(event compaction.ToolResultTrimEvent) []string {
	switch strings.TrimSpace(event.ToolName) {
	case "read_file":
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(event.Arguments, &payload); err != nil {
			return nil
		}
		return nonEmptyPathList(payload.Path)
	case "filesystem":
		var payload struct {
			Action string `json:"action"`
			Path   string `json:"path"`
		}
		if err := json.Unmarshal(event.Arguments, &payload); err != nil {
			return nil
		}
		if strings.ToLower(strings.TrimSpace(payload.Action)) != "read" {
			return nil
		}
		return nonEmptyPathList(payload.Path)
	default:
		return nil
	}
}

func nonEmptyPathList(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return []string{path}
}
